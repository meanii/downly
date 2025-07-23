from loguru import logger

import time
from datetime import datetime
from pyrogram import Client
from pyrogram import __version__
from pyrogram.raw.all import layer
from downly import __config__, rabbit_client, enable_rabbitmq_consumer_registry
from pathlib import Path

from downly.utils.bot_info import bot


class Downly(Client):
    """
    Downly custom client for Telegram bot.
    Inherits from pyrogram.Client and initializes with custom configurations.
    """

    def __init__(self):
        name = self.__class__.__name__.lower()

        self.telegram = __config__.telegram

        super().__init__(
            name,
            api_id=__config__.telegram.api_id,
            api_hash=__config__.telegram.api_hash,
            bot_token=__config__.telegram.bot_token,
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
        
        # disable RabbitMQ Consumer Registry
        enable_rabbitmq_consumer_registry.disable()
        logger.info("RabbitMQ Consumer Registry disabled.")
        
        # Close RabbitMQ connection
        rabbit_client.close()
        logger.info("RabbitMQ connection closed.")
        