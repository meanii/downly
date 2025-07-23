from pyrogram import filters, Client
from pyrogram.types import Message
from pyrogram.enums import ChatType

from downly.rabbitmq.registry import RabbitMQPublisherRegistry
from downly.rabbitmq.types import AvailablePublishers

from downly.downly import Downly
from downly.utils.b_logger import b_logger

from downly.database.users_sql import update_user, update_chat


@Downly.on_message(filters.private | filters.group | filters.channel, group=2)
@b_logger
async def logger(client: Client, message: Message):
    # check if a message is command then do nothing
    if message.chat.type == ChatType.GROUP or message.chat.type == ChatType.SUPERGROUP:
        # update_chat(str(message.chat.id), message.chat.title)
        
        RabbitMQPublisherRegistry.get_publisher(
            AvailablePublishers.DOWNLY_EVENT_STATS_UPDATER_PUBLISHER.name
        ).publish(
            routing_key=AvailablePublishers.DOWNLY_EVENT_STATS_UPDATER_PUBLISHER.routing_keys["chats"],
            body={
                "chat_id": str(message.chat.id),
                "chat_title": message.chat.title,
                "message_id": message.id,
                "from_user_id": message.from_user.id if message.from_user else None,
                "text": message.text or "",
            }
        )

    if message.from_user:
        # update_user(message.from_user.id, message.from_user.username)
        RabbitMQPublisherRegistry.get_publisher(
            AvailablePublishers.DOWNLY_EVENT_STATS_UPDATER_PUBLISHER.name
        ).publish(
            routing_key=AvailablePublishers.DOWNLY_EVENT_STATS_UPDATER_PUBLISHER.routing_keys["users"],
            body={
                "user_id": message.from_user.id,
                "username": message.from_user.username,
                "message_id": message.id,
                "text": message.text or "",
            }
        )
        
    await message.reply_text(
        "Your message has been logged and processed. Thank you!"
    )