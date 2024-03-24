from downly.downly import Downly
from downly.utils.b_logger import b_logger
from pyrogram import filters, Client
from pyrogram.types import Message


@Downly.on_message(filters.new_chat_members)
@b_logger
async def start(client: Client, message: Message):
    # get bot info
    bot_info = await client.get_me()

    welcome_message = (
        "Herzlich willkommen in der Gruppe!\n" "join @spookyanii for updates"
    )

    # check if user is added in new chat
    if bot_info.id in [user.id for user in message.new_chat_members]:
        await client.send_message(
            chat_id=message.chat.id,
            text=welcome_message,
            reply_to_message_id=message.id,
        )

