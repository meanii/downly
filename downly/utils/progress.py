from pyrogram.types import Message
from downly import get_logger

logger = get_logger(__name__)


class Progress:
    def __init__(self, message: Message):
        self.message = message
        self.total = 0
        self.current = 0

    async def progress(self, current: int, total: int):
        self.total = total
        self.current = current

        logger.info(
            f'uploading for {self.message.chat.title}({self.message.chat.id}) '
            f'{current * 100 / self.total:.1f}% '
            f'input: {self.message.text}'
        )

        try:
            await self.message.edit_text(
                f'uploading {current * 100 / self.total:.1f}%\nPlease have patience...'
            )
        except Exception as e:
            logger.error(f'Error while editing message for {self.message.text} - '
                         f'error message: {e}')
            return
