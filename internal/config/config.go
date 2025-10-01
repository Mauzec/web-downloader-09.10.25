package config

import (
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/spf13/viper"
)

type Config interface {
	Validate(v *viper.Viper) error
}

type AppConfig struct {
	ServerAddr                  string        `mapstructure:"SERVER_ADDR" validate:"min=2"`
	GinMode                     string        `mapstructure:"GIN_MODE" validate:"min=4"`
	FilesDir                    string        `mapstructure:"FILES_DIR" validate:"min=1"`
	SecDir                      string        `mapstructure:"SEC_DIR" validate:"min=1"`
	UserAgent                   string        `mapstructure:"USER_AGENT" validate:"min=1"`
	DownloadTimeout             time.Duration `mapstructure:"DOWNLOAD_TIMEOUT" validate:"nonzero_duration"`
	MaxCPU                      int           `mapstructure:"DOWNLOAD_MAX_CPU" validate:"min=1"`
	DownloadQueueSize           int           `mapstructure:"DOWNLOAD_QUEUE_SIZE" validate:"min=1"`
	RetryDownloadFileWait       time.Duration `mapstructure:"RETRY_DOWNLOAD_FILE_WAIT" validate:"nonzero_duration"`
	RetryDownloadFileMaxRetries int           `mapstructure:"RETRY_DOWNLOAD_FILE_MAX_RETRIES" validate:"min=1"`
	RetryAllWhenQueueFullWait   time.Duration `mapstructure:"RETRY_ALL_WHEN_QUEUE_FULL_WAIT" validate:"nonzero_duration"`
	SnapshotInterval            time.Duration `mapstructure:"SNAPSHOT_INTERVAL" validate:"nonzero_duration"`
	SnapshotTimeout             time.Duration `mapstructure:"SNAPSHOT_TIMEOUT" validate:"nonzero_duration"`
	StorageMode                 string        `mapstructure:"STORAGE_MODE" validate:"oneof=memory bbolt"`
}

func (c *AppConfig) Validate() error {
	v := validator.New()

	_ = v.RegisterValidation("nonzero_duration", func(fl validator.FieldLevel) bool {
		if d, ok := fl.Field().Interface().(time.Duration); ok {
			return d > 0
		} else {
			return false
		}
	})
	if err := v.Struct(c); err != nil {
		return err
	}
	return nil
}

func LoadAppConfig(name, ext string, paths ...string) (*AppConfig, error) {
	for _, path := range paths {
		viper.AddConfigPath(path)
	}
	viper.SetConfigName(name)
	viper.SetConfigType(ext)
	viper.AutomaticEnv()

	viper.SetDefault("SERVER_ADDR", ":8081")
	viper.SetDefault("GIN_MODE", "debug")
	viper.SetDefault("FILES_DIR", "./data/server/files")
	viper.SetDefault("SEC_DIR", "./data/server")
	viper.SetDefault("USER_AGENT", "Mozilla/5.0 (compatible; WebDownloader/1.0; +https://github.com/common)")
	viper.SetDefault("DOWNLOAD_TIMEOUT", 2*time.Minute)
	viper.SetDefault("DOWNLOAD_MAX_CPU", 4)
	viper.SetDefault("DOWNLOAD_QUEUE_SIZE", 32)
	viper.SetDefault("RETRY_DOWNLOAD_FILE_WAIT", 30*time.Second)
	viper.SetDefault("RETRY_DOWNLOAD_FILE_MAX_RETRIES", 5)
	viper.SetDefault("RETRY_ALL_WHEN_QUEUE_FULL_WAIT", 25*time.Second)
	viper.SetDefault("SNAPSHOT_INTERVAL", 6*time.Hour)
	viper.SetDefault("SNAPSHOT_TIMEOUT", time.Minute)
	viper.SetDefault("STORAGE_MODE", "bbolt")

	err := viper.ReadInConfig()

	if err != nil {
		return nil, err
	}
	cfg := &AppConfig{}
	err = viper.Unmarshal(cfg)
	if err != nil {
		return nil, err
	}
	if err = cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}
