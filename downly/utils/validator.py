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
