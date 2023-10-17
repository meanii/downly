import time
from datetime import datetime
from pyrogram import Client
from pyrogram import __version__
from pyrogram.raw.all import layer
from downly import telegram
from pathlib import Path

class Downly(Client):
    """
    Downly 🦉
    """
    def __init__(self):
        name = self.__class__.__name__.lower()

        self.telegram = telegram

        super().__init__(
            name,
            api_id=self.telegram.get('api_id'),
            api_hash=self.telegram.get('api_hash'),
            bot_token=self.telegram.get('bot_token'),
            workdir=str(Path.cwd()),
            workers=16,
            plugins=dict(
                root=f"{name}.plugins",
            ),
            sleep_threshold=180
        )

        self.uptime_reference = time.monotonic_ns()
        self.start_datetime = datetime.utcnow()

    async def start(self):
        await super().start()

        me = await self.get_me()
        print(f"Downly 🦉 v{__version__} (Layer {layer}) started on @{me.username}. Hi.")

    async def stop(self, *args):
        await super().stop()
        print("Downly 🦉 stopped. Bye.")