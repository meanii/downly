import json
from datetime import datetime
from ulid import ULID


class DownlyQueue:
    QUEUE_KEY = 'downly:queue'
    def __init__(self, redis):
        self.redis = redis
    
    def add(self, value: dict) -> dict:
        value['id'] = str(ULID())
        value['status'] = 'pending'
        value['created_at'] = str(datetime.now())
        self.redis.lpush(self.QUEUE_KEY, json.dumps(value))
        return value['id']

    def get_status(self, id: str) -> dict:
        for item in self.redis.lrange(self.QUEUE_KEY, 0, -1):
            item = json.loads(item)
            if item.get('id') == id:
                return item
        return None