import importlib
from .downly import Downly
from downly.database import ALL_DATABASES_MODULES

# load database
for module in ALL_DATABASES_MODULES:
    imported_module = importlib.import_module("downly.database." + module)

if __name__ == "__main__":
    Downly().run()  # running bot
