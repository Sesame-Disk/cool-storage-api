package configread_test

import (
	configread "cool-storage-api/configread"
	"testing"
)

func TestParseYamlConfig(t *testing.T) {

	config := configread.ParseYamlConfig("../conf/cool-api.yaml")
	if config.ServerConfig.Port != ":3001" || config.ServerConfig.TimeoutSecs != 10 || config.ServerConfig.ReadTimeoutSecs != 15 || config.ServerConfig.WriteTimeoutSecs != 15 {
		t.Fatalf("Expected to get  %s,%d,%d,%d but instead got %s,%d,%d,%d\n", ":3001", 10, 15, 15, config.ServerConfig.Port, config.ServerConfig.TimeoutSecs,
			config.ServerConfig.ReadTimeoutSecs, config.ServerConfig.WriteTimeoutSecs)
	}
	if config.CoolAppConf.PrefixUrl != "http://127.0.0.1:3001" {
		t.Fatalf("Expected to get  %s but instead got %s\n", "http://127.0.0.1:3001", config.CoolAppConf.PrefixUrl)
	}

}
