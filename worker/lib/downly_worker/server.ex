defmodule DownlyWorker.Server do
  @moduledoc false

  use GenServer
  require Logger

  def start_link(_) do
    Logger.info("Downly Worker starting...")
    GenServer.start_link(__MODULE__, %{})
  end

  def init(_default) do
    Logger.info("Downly Worker started. 🦉")
    DownlyWorker.start()
    {:ok, %{}}
  end


end
