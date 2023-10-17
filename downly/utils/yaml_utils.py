import sys
from pathlib import Path
from yaml import safe_load, YAMLError


def get_yaml(file_name: Path):
    try:
        with open(file_name, 'r') as f:
            return safe_load(f)
    except YAMLError:
        print(f'Error while parsing {file_name}')
        sys.exit(1)
