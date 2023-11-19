import httpx
from downly import get_logger
from downly.utils.fs_utils import make_sure_path_exists
from pathlib import Path

logger = get_logger(__name__)


class StreamDownloader:
    def __init__(self, url, output_path, chunk_size=1024):
        self.url = url
        self.output_path = output_path
        self.chunk_size = chunk_size
        self.client = httpx.AsyncClient()

    async def download(self):
        """
        download file from url in stream mode
        :return:
        """

        SLOW_DOWNLOAD_THRESHOLD = 300  # 300 bytes
        SLOW_DOWNLOAD_TRIES = 30  # 30 times

        make_sure_path_exists(Path(self.output_path).parent)  # make sure parent directory exists
        print(f"downloading {self.url} to {self.output_path}")
        async with self.client.stream("GET", self.url) as response:

            # get file name and extension from stream response
            file_name = response.headers.get("Content-Disposition").split("filename=")[1].replace('"', "")
            self.output_path = self.output_path.replace("[STREAM_FILENAME]", f"{file_name}")

            with open(self.output_path, "wb") as file:
                async for chunk in response.aiter_bytes():
                    logger.debug(f"writing {len(chunk)} bytes to {self.output_path}")

                    # check if download slow less than 1kb/s
                    if len(chunk) < SLOW_DOWNLOAD_THRESHOLD:
                        SLOW_DOWNLOAD_TRIES -= 1
                        if SLOW_DOWNLOAD_TRIES == 0:
                            logger.error(f"The download speed is insufficient, at less than {SLOW_DOWNLOAD_THRESHOLD} "
                                         f"bytes/s. The associated file has been removed.")
                            await self.delete()
                            raise Exception(f"The download speed is insufficient, at less than {SLOW_DOWNLOAD_THRESHOLD} "
                                            f"bytes/s. The associated file has been removed.")
                        logger.warning(f"The download speed is slow, less than 1kb/s. {SLOW_DOWNLOAD_TRIES} attempts "
                                       f"remaining.")

                    file.write(chunk)
                logger.info(f"finished writing {self.output_path}")
        await self.client.aclose()  # close client
        return self.output_path

    async def delete(self):
        """
        delete file
        :return:
        """
        Path(self.output_path).unlink(missing_ok=True)
        logger.info(f"deleted {self.output_path}")