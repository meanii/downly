from downly import configs
import httpx


def get_config(config: dict):
    """
    Download config for cobalt
    :param config:
    :return: dict
    """
    return ({
        "url": config.get("url"),
        "vCodec": "h264",
        "vQuality": "1080",
        "disableMetadata": "true",
        "dubLang": "false",
        "aFormat": "mp3",
        "filenamePattern": "nerdy"
    })


class CobaltEngine:
    def __init__(self, cobalt_base_url: str = configs.get('cobalt_base_url')):
        self.cobalt_base_url = cobalt_base_url

    def download(self, config: dict):
        r = httpx.post(
            f"{self.cobalt_base_url}/api/json",
            json=get_config(config),
            headers={"content-type": "application/json", "accept": "application/json"}
        )
        return r.json()