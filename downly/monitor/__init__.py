import threading
import psutil
import time
import os
from loguru import logger

class MonitoringTaskRunner:
    def __init__(self):
        self.active_monitors = set()
    
    def start_background_monitor(self, interval=5):
        """Start resource monitoring in background"""
        
        def monitor_resources():
            process = psutil.Process(os.getpid())
            
            while True:
                try:
                    cpu = process.cpu_percent()
                    memory = process.memory_info().rss / 1024 / 1024
                    threads = process.num_threads()
                    
                    logger.info(f"[MONITOR] CPU: {cpu}%, Memory: {memory:.1f}MB, Threads: {threads}")
                    time.sleep(interval)
                    
                except psutil.NoSuchProcess:
                    logger.info("[MONITOR] Process ended, monitoring stopped")
                    break
                except Exception as e:
                    logger.info(f"[MONITOR] Error: {e}")
                    break
        
        thread = threading.Thread(target=monitor_resources, daemon=True)
        self.active_monitors.add(thread)
        thread.start()
        return thread
