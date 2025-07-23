from loguru import logger
from pyrogram import filters, Client
from pyrogram.types import Message
from downly.utils.validator import validate_url, is_supported_service

from downly.downly import Downly
from downly.utils.b_logger import b_logger
from downly.utils.message import get_chat_info


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