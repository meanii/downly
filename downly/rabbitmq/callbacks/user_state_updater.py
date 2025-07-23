import json
from loguru import logger
from pika.adapters.blocking_connection import BlockingChannel

from downly.database.users_sql import update_user

def user_state_updator(channel: BlockingChannel, method, properties, body):
    """
    Sample callback function for processing messages.
    This should be replaced with actual logic.
    """
    try:
        logger.info(f"Received message: {body.decode()} on routing key: {method.routing_key} delivery_tag: {method.delivery_tag}")
        message = json.loads(body.decode())
        update_user(user_id=message.get('user_id'), username=message.get('username'))
        channel.basic_ack(method.delivery_tag)  # Acknowledge the message after processing
    except Exception as e:
        logger.critical("callback failed (method.delivery_tag: %s): %s", method.delivery_tag, e)
        channel.basic_nack(method.delivery_tag)
    