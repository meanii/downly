from pyrogram.types import Message
from downly import get_logger

logger = get_logger('bot') # logger for bot


def b_logger(func):
    async def wrapper(client, message: Message):
        logger.info(f"New message from {message.from_user.first_name}({message.from_user.id})"
                    f" in {message.chat.title}({message.chat.id}) -"
                    f" [MESSAGE]: {message.text}")
        return await func(client, message)

    return wrapper