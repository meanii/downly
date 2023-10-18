from pyrogram import filters
from downly import get_logger
from downly.downly import Downly
from downly.utils.b_logger import b_logger

logger = get_logger(__name__)


@Downly.on_message(filters.command(commands='start', prefixes='/'))
@b_logger
async def start(_, message):
    # content
    start_message = (
        f'hellow 🦉!\n\n'
        'downly is a simple telegram bot that can download files from the internet and upload them to telegram.\n'
        'downly uses [wukko/cobalt](https://github.com/wukko/cobalt/) to download files. downly is written in python using pyrogram.\n\n'
        'If you have any suggestions, please contact @aniicrite. and if you want to get updates about bot, join @spookyanii.\n\n'
        '- If you want to see the source code, visit [meanii/downly](https://github.com/meanii/downly).'
    )
    await message.reply_text(start_message, disable_web_page_preview=True)
