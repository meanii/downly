from loguru import logger
from typing import List, Optional, Union
from telegram import Bot, InputMedia, InputMediaPhoto, InputMediaVideo, InputMediaDocument
from telegram.request import HTTPXRequest
import asyncio
import time
from mimetypes import guess_type

class TelegramClient:
    _instance = None
    _last_request_time = 0
    REQUEST_INTERVAL = 0.5  # Minimum time between requests in seconds

    def __new__(cls, *args, **kwargs):
        if not cls._instance:
            cls._instance = super(TelegramClient, cls).__new__(cls)
        return cls._instance

    def __init__(self, token: str):
        if not hasattr(self, "token"):
            self.token = token
        if not hasattr(self, "loop"):
            self.loop = asyncio.new_event_loop()
            asyncio.set_event_loop(self.loop)

    def _create_bot(self) -> Bot:
        """Create a new Bot instance with proper HTTP configuration"""
        request = HTTPXRequest(
            connection_pool_size=20,
            pool_timeout=30.0
        )
        return Bot(token=self.token, request=request)

    def _convert_to_input_media(self, media_item: Union[str, InputMedia]) -> InputMedia:
        """Convert URL string to appropriate InputMedia object based on MIME type"""
        if isinstance(media_item, InputMedia):
            return media_item
        
        # Handle URL string
        mime_type, _ = guess_type(media_item)
        if mime_type:
            if mime_type.startswith('image/'):
                return InputMediaPhoto(media=media_item)
            elif mime_type.startswith('video/'):
                return InputMediaVideo(media=media_item)
        # Default to document for unknown types
        return InputMediaDocument(media=media_item)

    async def _send_media_async(self, processed_media: List[InputMedia], chat_id: int, 
                              message_id: Optional[int], caption: Optional[str]):
        """Actual async sending with rate limiting"""
        # Apply caption to the first media item if specified
        if caption and processed_media:
            processed_media[0].caption = caption
            
        # Rate limiting - ensure minimum time between requests
        elapsed = time.time() - self._last_request_time
        if elapsed < self.REQUEST_INTERVAL:
            await asyncio.sleep(self.REQUEST_INTERVAL - elapsed)
            
        bot = self._create_bot()
        await bot.send_media_group(
            chat_id=chat_id,
            reply_to_message_id=message_id,
            media=processed_media
        )
        self._last_request_time = time.time()

    def send_media(
        self,
        chat_id: int,
        media: List[Union[InputMedia, str]],
        message_id: Optional[int] = None,
        caption: Optional[str] = None,
    ):
        # Convert all media items to proper InputMedia objects
        processed_media = [self._convert_to_input_media(item) for item in media]
        
        logger.info(f"Sending media to chat_id: {chat_id}")
        
        # Create and run async task in the persistent event loop
        task = self.loop.create_task(
            self._send_media_async(processed_media, chat_id, message_id, caption)
        )
        self.loop.run_until_complete(task)

    def delete_message(self, chat_id: int, message_id: int):
        bot = self._create_bot()
        try:
            task = self.loop.create_task(bot.delete_message(chat_id=chat_id, message_id=message_id))
            self.loop.run_until_complete(task)
            logger.info(f"Message {message_id} deleted in chat {chat_id}")
        except Exception as e:
            logger.error(f"Failed to delete message {message_id} in chat {chat_id}: {e}")

    def edit_message(self, chat_id: int, message_id: int, new_text: str):
        bot = self._create_bot()
        try:
            task = self.loop.create_task(bot.edit_message(chat_id=chat_id, message_id=message_id, text=new_text))
            self.loop.run_until_complete(task)
            logger.info(f"Message {message_id} edited in chat {chat_id}")
        except Exception as e:
            logger.error(f"Failed to edit message {message_id} in chat {chat_id}: {e}")
    
    def send_message(self, chat_id: int, text: str, reply_to_message_id: Optional[int] = None):
        bot = self._create_bot()
        try:
            task = self.loop.create_task(bot.send_message(chat_id=chat_id, text=text, reply_to_message_id=reply_to_message_id))
            self.loop.run_until_complete(task)
            logger.info(f"Message sent to chat {chat_id}")
        except Exception as e:
            logger.error(f"Failed to send message to chat {chat_id}: {e}")