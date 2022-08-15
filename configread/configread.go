package configread

import (
	"os"

	"gopkg.in/yaml.v2"
)

type Config struct {
	CoolAppConf    AppConf  `yaml:"app"`
	ServerConfig   ServConf `yaml:"server"`
	DataBaseConfig DBConf   `yaml:"db"`
	AWSConfig      AWSConf  `yaml:"aws"`
}

type AppConf struct {
	PrefixUrl       string `yaml:"prefixUrl"`
	StorageSavePath string `yaml:"storageSavePath"`
}

type ServConf struct {
	Port             string `yaml:"port"`
	TimeoutSecs      int    `yaml:"timeoutSecs"`
	ReadTimeoutSecs  int    `yaml:"readTimeoutSecs"`
	WriteTimeoutSecs int    `yaml:"writeTimeoutSecs"`
}

type DBConf struct {
	Usuario           string      `yaml:"user"`
	Pass              string      `yaml:"pass"`
	Host              string      `yaml:"host"`
	NombreBaseDeDatos string      `yaml:"dataBaseName"`
	Migrate           MigrateConf `yaml:"migrate"`
	Pool              PoolConf    `yaml:"pool"`
}

type MigrateConf struct {
	Enable bool   `yaml:"enable"`
	Dir    string `yaml:"dir"`
}

type PoolConf struct {
	MaxOpen     int `yaml:"maxOpen"`
	MaxIdle     int `yaml:"maxIdle"`
	MaxLifetime int `yaml:"maxLifetime"`
}

type AWSConf struct {
	AccessKeyID     string `yaml:"accessKeyID"`
	SecretAccessKey string `yaml:"secretAccessKey"`
	AccessToken     string `yaml:"accessToken"`
	Region          string `yaml:"region"`
	VaultName       string `yaml:"vaultName"`
}

func unmarshalYAMLFile(path string, v interface{}) error {
	if path == "" {
		path = "conf/cool-api..yaml"
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return yaml.NewDecoder(f).Decode(v)
}

// read the Yaml into struct(s)
func ParseYamlConfig(pathYaml string) Config {
	conf1 := Config{}
	if err := unmarshalYAMLFile(pathYaml, &conf1); err != nil {
		panic(err)
	}
	return conf1
}

var Configuration = ParseYamlConfig("conf/cool-api.yaml")
