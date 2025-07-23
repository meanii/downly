import importlib
import uvloop

from downly.downly import Downly
from downly.database import ALL_DATABASES_MODULES


for module in ALL_DATABASES_MODULES:
    imported_module = importlib.import_module("downly.database." + module)


if __name__ == "__main__":
    uvloop.install()
    Downly().run()  # running bot
