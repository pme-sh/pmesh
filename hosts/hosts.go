package hosts

func Resolve(hostname Hostname) (Address, error) {
	cfg, err := SystemConfig()
	if err != nil {
		return "", err
	}
	adr, _ := cfg.Resolve(hostname)
	return adr, nil
}
func ResolveAll(hostname []Hostname) (Mapping, error) {
	cfg, err := SystemConfig()
	if err != nil {
		return nil, err
	}

	m := make(Mapping)
	for _, h := range hostname {
		adr, _ := cfg.Resolve(h)
		m[h] = adr
	}
	return m, nil
}

func Insert(entries Mapping) error {
	return UpdateSystemConfig(func(cfg *Config) error {
		changed := cfg.Insert(entries)
		if !changed {
			return ErrAbort
		}
		return nil
	})
}
