defmodule DownlyWorker.Handlers.Youtube do
  @moduledoc """
  Youtube handler module.
  This module is responsible for handling Youtube downloads.
  """
  require Logger

  def download(url, path) do
    Logger.info("Downloading #{url} to #{path}")
  end



end
