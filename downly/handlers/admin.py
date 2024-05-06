from functools import wraps


from pyrogram import enums
from pyrogram import Client
from pyrogram.types import Message

from downly.downly import Downly


def admins_only(func):
    @wraps(func)
    async def decorator(bot: Downly, message: Message):
        if await is_admin(bot, message.chat.id, message.from_user.id):
            await func(bot, message)
    return decorator

async def is_admin(client: Client, chat_id: int, user_id: int) -> bool:
    if chat_id == user_id:
        return True
    
    administrators = []
    async for m in client.get_chat_members(chat_id, filter=enums.ChatMembersFilter.ADMINISTRATORS):
        administrators.append(m)
    return administrators.__contains__(user_id)