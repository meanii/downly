from enum import Enum
from time import time

from loguru import logger
from pyrogram import filters, Client
from pyrogram.types import Message
from downly.utils.validator import validate_url, is_supported_service
from urllib.parse import urlparse

from downly.downly import Downly
from downly.rabbitmq.registry import RabbitMQPublisherRegistry, AvailablePublishers
from downly.utils.b_logger import b_logger
from downly.utils.message import get_chat_info

class ServiceType(Enum):
    COBALT = "cobalt"
    YTDL = "ytdl"


def detect_service(url: str) -> ServiceType:
    """
    delect service will check the supported service, and return the type
    """
    domain = urlparse(url).netloc.replace("www.", "")
    match domain:
        case "youtube.com":
            return ServiceType.YTDL
        case "youtu.be":
            return ServiceType.YTDL
        case _:
            return ServiceType.COBALT


@Downly.on_message(filters.private | filters.group | filters.channel, group=1)
@b_logger
async def download(client: Client, message: Message):
    # check if a message is command then do nothing
    if message.command:
        return

    if not message.text:
        return

    user_url_message = message.text

    # get chat info if a message is from a group or channel
    title, id = get_chat_info(message)

    # validating valid url by urllib
    if not validate_url(user_url_message):
        return

    # check if user is from available service
    if not is_supported_service(user_url_message):
        logger.warning(f"unsupported service {user_url_message}")
        return

    logger.info(
        f"New message from {message.from_user.first_name}({message.from_user.id})"
        f" in {title}({id}) - [MESSAGE]: {user_url_message}"
    )
    
    replied_message = await message.reply_text(
        "Your download request has been received. "
        "Please wait while we process your request.",
        quote=True
    )
    
    RabbitMQPublisherRegistry.get_publisher(
            AvailablePublishers.DOWNLY_WORKER_QUEUE_PUBLISHER.name
        ).publish(
            routing_key=AvailablePublishers.DOWNLY_WORKER_QUEUE_PUBLISHER.routing_keys.get(
                detect_service(user_url_message).value
            ),
            body={
                "chat_id": str(message.chat.id),
                "chat_title": message.chat.title,
                "message_id": message.id,
                "from_user_id": message.from_user.id if message.from_user else None,
                "url": user_url_message,
                "timestamp": f"{time():.0f}",
                "replied_message_id": replied_message.id
            }
    )