package config

import "testing"

func TestPoolConfig_DefaultsDisabled(t *testing.T) {
	c := &Config{Fleet: FleetConfig{Environment: "dev"}}
	c.Defaults()

	// Pool is disabled by default (PoolSize == 0).
	if c.Pool.PoolSize != 0 {
		t.Errorf("Pool.PoolSize = %d, want 0 (disabled by default)", c.Pool.PoolSize)
	}
	// Table name is derived even when disabled so operators can pre-create it.
	want := "ai-desktops-pool-dev"
	if c.Pool.TableName != want {
		t.Errorf("Pool.TableName = %q, want %q", c.Pool.TableName, want)
	}
}

func TestPoolConfig_SizeZeroMeansDisabled(t *testing.T) {
	c := &Config{}
	c.Pool.PoolSize = 0
	if c.Pool.IsEnabled() {
		t.Error("PoolSize=0 should report IsEnabled()=false")
	}
}

func TestPoolConfig_SizePositiveMeansEnabled(t *testing.T) {
	c := &Config{}
	c.Pool.PoolSize = 2
	if !c.Pool.IsEnabled() {
		t.Error("PoolSize=2 should report IsEnabled()=true")
	}
}

func TestPoolConfig_ExplicitTableName(t *testing.T) {
	c := &Config{
		Fleet: FleetConfig{Environment: "prod"},
		Pool:  PoolConfig{TableName: "custom-pool-table"},
	}
	c.Defaults()
	if c.Pool.TableName != "custom-pool-table" {
		t.Errorf("explicit TableName overridden: got %q", c.Pool.TableName)
	}
}
