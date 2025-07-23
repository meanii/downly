import functools
import threading


def fire_and_forget(func):
    """Decorator to run function in background thread"""
    @functools.wraps(func)
    def wrapper(*args, **kwargs):
        thread = threading.Thread(
            target=func, 
            args=args, 
            kwargs=kwargs, 
            daemon=True
        )
        thread.start()
        return thread
    return wrapper