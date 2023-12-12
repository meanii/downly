defmodule DownlyWorker.MixProject do
  use Mix.Project

  def project do
    [
      app: :downly_worker,
      version: "0.1.0",
      elixir: "~> 1.15",
      start_permanent: Mix.env() == :prod,
      deps: deps()
    ]
  end

  # Run "mix help compile.app" to learn about applications.
  def application do
    [
      extra_applications: [:logger],
      mod: {DownlyWorker.Application, []}
    ]
  end

  # Run "mix help deps" to learn about dependencies.
  defp deps do
    [
      {:poison, "~> 5.0"},
      {:redix, "~> 1.1"},
      {:castore, ">= 0.0.0"},
      {:yaml_elixir, "~> 2.9"},
      {:telegram, github: "visciang/telegram", tag: "1.2.1"},
      {:exyt_dlp, "~> 0.1.2"},
      {:poolboy, "~> 1.5.1"}
    ]
  end
end
