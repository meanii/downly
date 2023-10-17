from downly.utils.yaml_utils import get_yaml
from pathlib import Path

config = get_yaml(Path.resolve(Path.cwd() / "config.yaml"))

telegram = config.get('downly').get('telegram') # telegram configs, api_id, api_hash, bot_token
configs = config.get('downly').get('configs') # configs for plugins

