from sqlalchemy import create_engine
from sqlalchemy.ext.declarative import declarative_base
from sqlalchemy.orm import sessionmaker, scoped_session

from sqlalchemy_utils import database_exists, create_database

from downly import database_configs, get_logger

logger = get_logger(__name__)
BASE = declarative_base()


def start() -> scoped_session:
    postgres_url = database_configs.get("postgres_url")
    engine = create_engine(postgres_url, client_encoding="utf8")
    logger.info(f"[PostgreSQL] connecting to database... {engine.url}")

    if not database_exists(engine.url):
        logger.warning("database doesn't exist, creating new one...")
        create_database(engine.url)

    BASE.metadata.bind = engine
    BASE.metadata.create_all(bind=engine, checkfirst=True)
    return scoped_session(sessionmaker(bind=engine, autocommit=False, autoflush=False))


try:
    SESSION = start()
except Exception as e:
    logger.exception(f"[PostgreSQL] Failed to connect due to {e}")
    exit()

logger.info("[PostgreSQL] Connection successful, session started.")


def __get_all_databases():
    from os.path import dirname, basename, isfile
    import glob

    # This generates a list of modules in this folder for the * in __init__ to work.
    mod_paths = glob.glob(dirname(__file__) + "/*.py")
    all_modules = [
        basename(f)[:-3]
        for f in mod_paths
        if isfile(f) and f.endswith(".py") and not f.endswith("__init__.py")
    ]

    return all_modules


ALL_DATABASES_MODULES = sorted(__get_all_databases())
