package config

// LoadAdmin читает файл pod, в котором оператор запустил orders-admin (kubectl
// exec): сверяет apiVersion и kind, но не metadata.name — файл принадлежит роли
// pod. Неизвестные ключи отклоняются, как у процессов; секции команда
// проверяет сама. service.name — serviceName команды, а не роли pod.
func LoadAdmin(serviceName string) (Config, error) {
	cfg, err := read(serviceName, nil)
	if err != nil {
		return Config{}, err
	}
	cfg.Observability.Telemetry.ServiceName = serviceName
	if err := cfg.Observability.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
