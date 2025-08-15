import json
from loguru import logger
from downly.clients.telegram import TelegramClient
from downly.config import Config
from pika.adapters.blocking_connection import BlockingChannel

__config__ = Config().get_instance()
  
def downly_success_event(channel: BlockingChannel, method, _properties, body):
    """
    Callback function for processing successful events.
    """
    try:
        logger.info(f"Received message: {body.decode()} on routing key: {method.routing_key} delivery_tag: {method.delivery_tag}")
        message = json.loads(body.decode())
        logger.info(f"Processing message: {message}")
        logger.info(f"Sending media to chat_id: {message['chat_id']} with links: {message['links']}")
        bot = TelegramClient(
            token=__config__.telegram.bot_token
        )
        bot.send_media(
            chat_id=message['chat_id'],
            media=message['links'],
            message_id=message.get('message_id'),
            caption=message.get('caption'),
        )
        bot.delete_message(
            chat_id=message['chat_id'],
            message_id=message.get('replied_message_id'),
        )
        channel.basic_ack(method.delivery_tag)
    except Exception as e:
        logger.critical(f"Callback failed (method.delivery_tag: {e}")
        try:
            channel.basic_nack(method.delivery_tag, requeue=False)
        except Exception as nack_error:
            logger.critical(f"Failed to NACK in exception handler: {nack_error}")