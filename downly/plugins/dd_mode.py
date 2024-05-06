from pyrogram import filters, Client
from pyrogram.types import Message
from downly import get_logger
from downly.downly import Downly
from downly.utils.b_logger import b_logger
from downly.handlers.admin import admins_only

logger = get_logger(__name__)

@Downly.on_message(filters.command(commands="dd", prefixes="/"))
@admins_only
@b_logger
async def dd_mode(client: Client, message: Message):
    VALID_TOGGLE = ['on', 'off']
    if len(message.command) != 2:
        await message.reply(
            text="please provide a valid argument in order to use this command!", 
            quote=True
        )
        return
    
    if not VALID_TOGGLE.__contains__(message.command[1]):
        await message.reply(
            text=f"unexpected argument provided! `{message.command[1]}`\nargument should be: `{', '.join(VALID_TOGGLE)}`"
        )
    
    
    