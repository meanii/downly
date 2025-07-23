from sys import exit
from loguru import logger
from typing import Optional
from sqlalchemy import create_engine
from sqlalchemy.ext.declarative import declarative_base
from sqlalchemy.orm import sessionmaker, scoped_session

from sqlalchemy_utils import database_exists, create_database
from downly.config import Config

BASE = declarative_base()
SESSION: Optional[scoped_session] = None


def start() -> scoped_session:
    """
    Start the PostgreSQL database connection and create a session.
    """
    postgres_url = Config.get_instance().database.postgres_url     
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
    """
    Get all database modules in the current directory.
    This is used to dynamically import all database modules for the * in __init__ to work
    """
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
