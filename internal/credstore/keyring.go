package credstore

import (
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

// Keyring stores credentials in macOS Keychain or the Linux Secret Service.
type Keyring struct {
	GOOS string
}

func (k Keyring) Get(key string) (string, error) {
	v, err := keyring.Get(service, key)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", k.Name(), err)
	}
	return v, nil
}

func (k Keyring) Set(key, value string) error {
	if err := keyring.Set(service, key, value); err != nil {
		return fmt.Errorf("write %s: %w", k.Name(), err)
	}
	return nil
}

func (k Keyring) Delete(key string) error {
	err := keyring.Delete(service, key)
	if errors.Is(err, keyring.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("delete from %s: %w", k.Name(), err)
	}
	return nil
}

func (k Keyring) Name() string {
	if k.GOOS == "darwin" {
		return "macOS Keychain"
	}
	return "Secret Service"
}
