
import threading
from time import sleep
from loguru import logger
from downly.clients.rabbitmq import RabbitMQConnectionManager
from typing import Callable, Dict, Any

class RabbitMQConsumer:
    """
    Enhanced RabbitMQ consumer with built-in thread management and error handling.
    """
    def __init__(
        self,
        connection_manager: RabbitMQConnectionManager,
        queue: str,
        exchange: str,
        routing_key: str,
        callback: Callable[[str, Dict, Any, bytes], None],
        durable: bool = True
    ):
        self.connection_manager = connection_manager
        self.queue = queue
        self.exchange = exchange
        self.routing_key = routing_key
        self.callback = callback
        self.durable = durable
        self._consumer_thread = None
        self._stop_event = threading.Event()

    def _setup(self):
        """Ensure queue and bindings exist"""
        with self.connection_manager.lock:
            self.connection_manager.ensure_connection()
            channel = self.connection_manager.channel
            
            if not channel or not channel.is_open:
                logger.error("Channel not available for consumer setup")
                return False
            
            try:

                # Ensure the queue exists
                channel.queue_declare(queue=self.queue, durable=self.durable)
                
                # Ensure the exchange exists
                channel.exchange_declare(exchange=self.exchange, exchange_type='topic', durable=self.durable)

                # Bind the queue to the exchange
                channel.queue_bind(
                    exchange=self.exchange,
                    queue=self.queue,
                    routing_key=self.routing_key
                )
                
                return True
            except Exception as e:
                logger.error(f"Consumer setup failed: {e}")
                return False

    def _consume_loop(self):
        """Main consumption loop with reconnection handling"""
        logger.info(f"Starting consumer for queue: {self.queue}")
        
        while not self._stop_event.is_set():
            try:
                # Re-setup on each iteration to handle potential reconnections
                if not self._setup():
                    sleep(2)
                    continue
                
                with self.connection_manager.lock:
                    channel = self.connection_manager.channel
                    if not channel or not channel.is_open:
                        continue
                    
                    # Get a single message with timeout
                    method, properties, body = channel.basic_get(
                        queue=self.queue,
                        auto_ack=False
                    )
                    
                    if method:
                        try:
                            self.callback(channel, method, properties, body)
                        except Exception as e:
                            logger.error(f"Message processing failed: {e}")
                            channel.basic_nack(method.delivery_tag, requeue=False)
                    else:
                        # No message, sleep briefly
                        sleep(0.1)
            
            except Exception as e:
                logger.error(f"Consumer error: {e}")
                sleep(2)

    def start(self):
        """Start the consumer in a background thread"""
        if self._consumer_thread and self._consumer_thread.is_alive():
            logger.warning(f"Consumer for {self.queue} is already running")
            return
        
        self._stop_event.clear()
        self._consumer_thread = threading.Thread(
            target=self._consume_loop,
            daemon=True
        )
        self._consumer_thread.start()
        logger.info(f"Consumer started for queue: {self.queue}")

    def stop(self):
        """Stop the consumer gracefully"""
        self._stop_event.set()
        if self._consumer_thread and self._consumer_thread.is_alive():
            self._consumer_thread.join(timeout=5)
            logger.info(f"Consumer stopped for queue: {self.queue}")
