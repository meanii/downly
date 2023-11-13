from pyrogram import filters, Client
from pyrogram.types import Message
from pyrogram.enums import ChatType

from downly.downly import Downly
from downly.utils.b_logger import b_logger

from downly.database.users_sql import update_user, update_chat


@Downly.on_message(filters.private | filters.group | filters.channel, group=2)
@b_logger
async def logger(client: Client, message: Message):
    # check if a message is command then do nothing
    if message.chat.type == ChatType.GROUP or message.chat.type == ChatType.SUPERGROUP:
        update_chat(str(message.chat.id), message.chat.title)

    if message.chat.type == ChatType.PRIVATE:
        update_user(message.from_user.id, message.from_user.username)
