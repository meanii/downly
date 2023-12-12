defmodule DownlyWorker.Application do
  # See https://hexdocs.pm/elixir/Application.html
  # for more information on OTP Applications
  @moduledoc false

  use Application

  defp poolboy_config do
    [
      name: {:local, :worker},
      worker_module: DownlyWorker.Worker,
      size: 5,
      max_overflow: 2
    ]
  end

  @impl true
  def start(_type, _args) do
    children = [
      :poolboy.child_spec(:worker, poolboy_config()),
      {Redix, host: Application.get_env(:downly_worker, :redis_host), name: :redix},
      {DownlyWorker.Server, []}
    ]

    # See https://hexdocs.pm/elixir/Supervisor.html
    # for other strategies and supported options
    opts = [strategy: :one_for_one, name: DownlyWorker.Supervisor]
    Supervisor.start_link(children, opts)
  end

end
