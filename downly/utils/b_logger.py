from pyrogram.types import Message
from downly import get_logger
from downly.utils.validator import validate_url


logger = get_logger('bot')  # logger for bot


def b_logger(func):
    async def wrapper(client, message: Message):

        # checking if a message is url then log
        if not validate_url(message.text):
            await func(client, message)
            return

        # logging message
        if message.from_user: # if a message is from a user
            logger.info(f"New message from {message.from_user.first_name}({message.from_user.id})"
                        f" in {message.chat.title}({message.chat.id}) -"
                        f" [MESSAGE]: {message.text}")

        if message.from_user is None: # if a message is from channel
            logger.info(f"New message from {message.chat.title}({message.chat.id}) -"
                        f" [MESSAGE]: {message.text}")

        return await func(client, message)

    return wrapper