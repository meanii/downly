import Config

config_file = Path.join File.cwd!(), "../config.yaml"
case YamlElixir.read_from_file(config_file) do
  {:ok, content} ->
    config :downly_worker, :telegram_token, content["downly"]["telegram"]["bot_token"]
    config :downly_worker, :redis_host, content["downly"]["database"]["redis"]["host"]
  {:error, _} ->
    IO.puts "Failed to read config file"
end
