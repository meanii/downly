from pyrogram.types import Message
import yt_dlp as youtube_dl
from pathlib import Path
from downly import get_logger

logger = get_logger(__name__)


def progress_hook(d):
    if d["status"] == "finished":
        logger.info(f"finished downloading {d['filename']}")
    if d["status"] == "downloading":
        logger.info(f"downloading {d['filename']} {d['_percent_str']} {d['_eta_str']}")


class YoutubeDownloader:
    output_file = None

    def __init__(self, youtube_url, output_dir):
        self.youtube_url = youtube_url
        self.output_dir = output_dir

    async def download(self):
        ydl_opts = {
            "format": "bestvideo[ext=mp4]+bestaudio[ext=m4a]/best[ext=mp4]/best",
            "outtmpl": f"{self.output_dir}/%(title)s.%(ext)s",
        }
        with youtube_dl.YoutubeDL(ydl_opts) as ydl:
            # add progress hook
            ydl.add_progress_hook(progress_hook)

            info_dict = ydl.extract_info(self.youtube_url, download=True)
            video_title = ydl.prepare_filename(info_dict)
            self.output_file = Path.resolve(self.output_dir / video_title)
            return self.output_file

    async def delete(self):
        """
        delete file
        :return:
        """
        Path(self.output_file).unlink(missing_ok=True)
        logger.info(f"deleted {self.output_file}")
