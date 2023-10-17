import os
import errno


def make_sure_path_exists(path):
    """
    Create directory if it doesn't exist
    :param path:
    :return:
    """
    try:
        os.makedirs(path, exist_ok=True)
    except OSError as exception:
        if exception.errno != errno.EEXIST:
            raise

