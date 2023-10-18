from downly import configs, get_logger
import httpx

logger = get_logger(__name__)


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


    MAX_RETRIES = 5

    def __init__(self, cobalt_base_url: str = configs.get('cobalt_base_url')):
        self.cobalt_base_url = cobalt_base_url

    def download(self, config: dict):
        """
        Download video from cobalt
        that's keep retrying if failed
        for MAX_RETRIES times
        :param config:
        :return: dict
        """
        try:
            r = self.download_(config)
            CobaltEngine.MAX_RETRIES = 5  # reset
            return r
        except Exception as e:
            logger.warning(f'Error while downloading {config.get("url")}\n')
            CobaltEngine.MAX_RETRIES -= 1
            logger.warning(f'number of retries left: {CobaltEngine.MAX_RETRIES}\n')
            if CobaltEngine.MAX_RETRIES == 0: # if no retries left
                CobaltEngine.MAX_RETRIES = 5 # reset
                raise Exception(e)
            return self.download(config)

    def download_(self, config: dict):
        r = httpx.post(
            f"{self.cobalt_base_url}/api/json",
            json=get_config(config),
            headers={"content-type": "application/json", "accept": "application/json"}
        )
        return r.json()