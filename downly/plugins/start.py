from pyrogram import filters
from downly.downly import Downly


@Downly.on_message(filters.command(commands='start', prefixes='/'))
async def start(_, message):
    # content
    start_message = (
        f'hellow 🦉!\n\n'
        'I am a bot that can help you to save what you love.\n'
    )
    await message.reply_text(start_message)
