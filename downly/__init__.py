import logging.config

from pathlib import Path
from downly.utils.yaml_utils import get_yaml
from downly.utils.bot_info import bot

config = get_yaml(Path.resolve(Path.cwd() / "config.yaml"))

telegram = config.get('downly').get('telegram')  # telegram configs, api_id, api_hash, bot_token
configs = config.get('downly').get('configs')  # configs for plugins
database_configs = config.get('downly').get('database')  # database configs, postgresql and redis

# logging configs
logging.config.fileConfig(fname='logger.conf', disable_existing_loggers=False)


def get_logger(name: str):
    return logging.getLogger(name)
