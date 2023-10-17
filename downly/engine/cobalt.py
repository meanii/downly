from downly import configs
import httpx


def get_config(config: set):
    """
    Download config for cobalt
    :param config:
    :return: dict
    """
    return ({
        "url": config.url,
        "vCodec": "h264",
        "vQuality": "1080",
        "aFormat": "mp3",
        "filenamePattern": "nerdy"
    })


class CobaltEngine:
    def __init__(self, cobalt_base_url: str = configs.cobalt_base_url):
        self.cobalt_base_url = cobalt_base_url

    def download(self, config: set):
        r = httpx.post(f"{self.cobalt_base_url}/api/json", data=get_config(config))
        return r.json()