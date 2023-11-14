from pyrogram import filters
from downly import get_logger, configs
from downly.downly import Downly
from downly.utils.b_logger import b_logger

from downly.database.users_sql import count_users, count_chats

logger = get_logger(__name__)


@Downly.on_message(filters.command(commands='stats', prefixes='/'))
@b_logger
async def stats(_, message):
    OWNER_ID = str(configs.get('owner'))

    if not str(message.from_user.id) == OWNER_ID:
        return

    stats_message = (
        f'**Stats:**\n\n'
        f'total users is `{count_users()}` in `{count_chats()}` chats.\n'
        f'total number of downloads is `NOT_AVAILABLE_RN`.\n'
    )
    await message.reply_text(stats_message, disable_web_page_preview=True)