from urllib.parse import urlparse


def validate_url(url: str) -> bool:
    """
    Validate url
    :param url:
    :return: bool
    """
    try:
        result = urlparse(url)
        if all([result.scheme, result.netloc, result.path]):
            return True
        else:
            return False
    except ValueError:
        return False


def is_supported_service(service: str) -> bool:
    """
    Validate service
    :param service:
    :return: bool
    """

    domain = urlparse(service).hostname.replace('www.', '')
    AVAILABLE_SERVICES = [
        'bilibili.com', 'youtube.com', 'youtu.be', 'tiktok.com', 'twitter.com', 'instagram.com',
        'pinterest.com', 'reddit.com', 'rutube.ru', 'vimeo.com', 'soundcloud.com',
        'vine.co', 'dailymotion.com', 'vk.com', 'tumblr.com'
    ]
    return domain in AVAILABLE_SERVICES
