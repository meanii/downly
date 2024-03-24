import time
from datetime import datetime
from pyrogram import Client
from pyrogram import __version__
from pyrogram.raw.all import layer
from downly import telegram, get_logger
from pathlib import Path

from downly.utils.bot_info import bot

logger = get_logger(__name__)


class Downly(Client):
    """
    Downly 🦉
    """

    def __init__(self):
        name = self.__class__.__name__.lower()

        self.telegram = telegram

        super().__init__(
            name,
            api_id=self.telegram.get("api_id"),
            api_hash=self.telegram.get("api_hash"),
            bot_token=self.telegram.get("bot_token"),
            workdir=str(Path.cwd()),
            workers=16,
            plugins=dict(
                root=f"{name}.plugins",
            ),
            sleep_threshold=180,
        )

        self.uptime_reference = time.monotonic_ns()
        self.start_datetime = datetime.now()

    async def start(self):
        await super().start()

        me = await self.get_me()
        bot.username = me.username
        bot.id = me.id
        logger.info(
            f"Downly 🦉 v{__version__} (Layer {layer}) started on @{me.username}. Hi."
        )

    async def stop(self, *args):
        await super().stop()
        logger.info("Downly 🦉 stopped. Bye.")

