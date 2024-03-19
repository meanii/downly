from pyrogram import filters
from downly import get_logger, configs
from downly.downly import Downly
from downly.utils.b_logger import b_logger

from downly.database.users_sql import (
    count_users,
    count_chats,
    count_last_24_hours_users,
    count_last_24_hours_chats,
)
from downly.database.downloads_sql import count_downloads, count_last_24_hours_downloads

logger = get_logger(__name__)


@Downly.on_message(filters.command(commands="stats", prefixes="/"))
@b_logger
async def stats(_, message):
    OWNER_ID = str(configs.get("owner"))

    if not str(message.from_user.id) == OWNER_ID:
        return

    stats_message = (
        f"We currently have `{count_users()}` users spread across `{count_chats()}` chats. "
        f"The total download count stands at `{count_downloads()}`.\n\n"
        f"In the last 24 hours alone, there were `{count_last_24_hours_users()}` new users joining, "
        f"engaging in `{count_last_24_hours_chats()}` chats, and `{count_last_24_hours_downloads()}` downloads."
    )

    await message.reply_text(stats_message, disable_web_page_preview=True)

