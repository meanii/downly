from pyrogram import filters
from downly.downly import Downly
from downly.utils.b_logger import b_logger



@Downly.on_message(filters.command(commands="start", prefixes="/"))
@b_logger
async def start(_, message):
    # content
    start_message = (
        "Hello! I am Downly, a simple Telegram bot that can download files from the internet and upload them to "
        "Telegram.\n\n"
        "To use me, simply send me a link to the file you want to download, and I will take care of the rest. I can "
        "download files of any size, and I support a variety of services, including:\n\n"
        "• YouTube and Youtube Shots\n"
        "• Twitter\n"
        "• Instagram Post, Stories and Reels\n"
        "• Vimeo\n"
        "• SoundCloud\n"
        "• Bandcamp\n"
        "• Twitch\n"
        "• DailyMotion\n"
        "• TikTok\n"
        "• And many more!\n\n"
        "If you have any questions or suggestions, please feel free to contact me at @aniicrite. For updates about "
        "the bot, join @spookyanii.\n\n"
    )
    await message.reply_text(start_message, disable_web_page_preview=True)

