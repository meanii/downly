from typing import Tuple

from pyrogram.types import Message


def get_chat_info(message: Message) -> tuple[str | None, int]:
    if message.from_user:
        return (message.from_user.first_name, message.from_user.id)

    if message.from_user is None:
        return (message.chat.title, message.chat.id)

