from loguru import logger
from pathlib import Path
from downly.rabbitmq.connection import initialize_rabbitmq_client
from downly.models.config import load_config_from_yaml, DownlyConfig
import sys


# Load configuration
try:
    config_path = Path.resolve(Path.cwd() / "config.yaml")
    __config__: DownlyConfig = load_config_from_yaml(config_path)
    logger.info(f"Loaded configuration from {config_path}")
except Exception as e:
    logger.critical(f"Failed to load configuration: {e}")
    sys.exit(1)

# Initialize RabbitMQ client
rabbitmq_client = initialize_rabbitmq_client(__config__)