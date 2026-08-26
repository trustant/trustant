// Copyright 2025-2026 Nuvolaris Inc
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestWriteServiceWrapperReplacesSymlinkWithoutFollowingTarget(t *testing.T) {
	binDir := t.TempDir()
	upstream := filepath.Join(t.TempDir(), "package-managed-milvus-cli")
	if err := os.WriteFile(upstream, []byte("upstream\n"), 0755); err != nil {
		t.Fatalf("write upstream target: %s", err)
	}
	wrapper := filepath.Join(binDir, "milvus_cli")
	if err := os.Symlink(upstream, wrapper); err != nil {
		t.Fatalf("create legacy symlink: %s", err)
	}

	if err := writeServiceWrapper(binDir, "milvus_cli", "#!/bin/sh\nexit 0\n"); err != nil {
		t.Fatalf("writeServiceWrapper: %s", err)
	}
	info, err := os.Lstat(wrapper)
	if err != nil {
		t.Fatalf("lstat wrapper: %s", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0755 {
		t.Fatalf("wrapper must be a regular 0755 file, mode=%s", info.Mode())
	}
	targetData, err := os.ReadFile(upstream)
	if err != nil {
		t.Fatalf("read upstream target: %s", err)
	}
	if string(targetData) != "upstream\n" {
		t.Fatalf("package-managed symlink target was overwritten: %q", targetData)
	}
}

func TestRenderMilvusCliWrapperUsesExactGlobalEntryPoint(t *testing.T) {
	globalClient := filepath.Join(t.TempDir(), "milvus_cli")
	if err := os.WriteFile(globalClient, []byte("#!/opt/uv/tools/milvus-cli/bin/python\n"), 0755); err != nil {
		t.Fatalf("write global client: %s", err)
	}
	cfg := &opsConfig{}
	cfg.Milvus.Host = "milvus"
	cfg.Milvus.Port = 19530
	cfg.Milvus.DB.Name = "demo"

	body, err := renderMilvusCliWrapper(globalClient, cfg)
	if err != nil {
		t.Fatalf("renderMilvusCliWrapper: %s", err)
	}
	if !strings.HasPrefix(body, "#!/opt/uv/tools/milvus-cli/bin/python\n") {
		t.Fatalf("wrapper did not use the global entry point interpreter: %s", body)
	}
}

func TestRenderMilvusCliWrapperFailsWhenGlobalEntryPointIsMissing(t *testing.T) {
	_, err := renderMilvusCliWrapper(filepath.Join(t.TempDir(), "missing"), &opsConfig{})
	if err == nil || !strings.Contains(err.Error(), "read global milvus_cli") {
		t.Fatalf("expected explicit missing-global-client error, got %v", err)
	}
}

func TestServiceWrappersMatchMCPServiceEndpoints(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalClient := filepath.Join(t.TempDir(), "milvus_cli")
	if err := os.WriteFile(globalClient, []byte("#!/opt/uv/tools/milvus-cli/bin/python\n"), 0755); err != nil {
		t.Fatalf("write global Milvus client: %s", err)
	}

	cfg := &opsConfig{}
	cfg.S3.Host = "seaweedfs"
	cfg.S3.Port = 9000
	cfg.S3.Access.Key = "access-key"
	cfg.S3.Secret.Key = "secret-key"
	cfg.S3.Bucket.Static = "demo-web"
	cfg.S3.Bucket.Data = "demo-data"
	cfg.Postgres.Database = "demo"
	cfg.Postgres.URL = "postgresql://demo:pw@postgres:5432/demo"
	cfg.Redis.URL = "redis://redis:6379"
	cfg.Redis.Service = "redis"
	cfg.Redis.Port = 6379
	cfg.Redis.Password = "redis-password"
	cfg.Redis.Prefix = "demo:"
	cfg.Milvus.Host = "milvus"
	cfg.Milvus.Port = 19530
	cfg.Milvus.Token = "demo:milvus-token"
	cfg.Milvus.DB.Name = "demo"

	if err := setupServiceToolingFromConfigWithMilvusEntryPoint(cfg, globalClient); err != nil {
		t.Fatalf("setup service tooling: %s", err)
	}
	mcp := buildMCPFromOpsConfig(cfg)
	binDir := filepath.Join(home, ".local", "bin")

	assertWrapper := func(name string) string {
		t.Helper()
		path := filepath.Join(binDir, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s wrapper: %s", name, err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0755 {
			t.Fatalf("%s wrapper must be a regular 0755 executable, mode=%s", name, info.Mode())
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s wrapper: %s", name, err)
		}
		return string(body)
	}

	s3 := mcp["s3"].(map[string]interface{})
	s3Env := s3["environment"].(map[string]string)
	rclone := assertWrapper("rclone")
	if !strings.Contains(rclone, s3Env["S3_ENDPOINT"]) ||
		!strings.Contains(rclone, cfg.S3.Bucket.Static) ||
		!strings.Contains(rclone, cfg.S3.Bucket.Data) {
		t.Fatalf("rclone wrapper does not match S3 MCP/config endpoint: %s", rclone)
	}

	postgres := mcp["postgres"].(map[string]interface{})
	postgresEnv := postgres["environment"].(map[string]string)
	if psql := assertWrapper("psql"); !strings.Contains(psql, postgresEnv["DATABASE_URI"]) {
		t.Fatalf("psql wrapper does not match PostgreSQL MCP endpoint: %s", psql)
	}

	redis := mcp["redis"].(map[string]interface{})
	redisEnv := redis["environment"].(map[string]string)
	redisCLI := assertWrapper("redis-cli")
	for _, endpointPart := range []string{
		redisEnv["REDIS_HOST"],
		redisEnv["REDIS_PORT"],
		redisEnv["REDIS_USERNAME"],
	} {
		if !strings.Contains(redisCLI, endpointPart) {
			t.Fatalf("redis-cli wrapper does not match Redis MCP endpoint %q: %s", endpointPart, redisCLI)
		}
	}

	milvus := mcp["milvus"].(map[string]interface{})
	milvusArgs := milvus["command"].([]string)
	milvusCLI := assertWrapper("milvus_cli")
	for _, endpointPart := range []string{
		cfg.Milvus.Host,
		strconv.Itoa(cfg.Milvus.Port),
		cfg.Milvus.DB.Name,
	} {
		if !strings.Contains(milvusCLI, endpointPart) {
			t.Fatalf("milvus_cli wrapper is missing configured value %q: %s", endpointPart, milvusCLI)
		}
	}
	if !strings.Contains(strings.Join(milvusArgs, " "), cfg.Milvus.Host+":"+strconv.Itoa(cfg.Milvus.Port)) {
		t.Fatalf("Milvus MCP args do not match wrapper endpoint: %#v", milvusArgs)
	}
}

func TestServiceToolingFailureDoesNotExposeMilvusToken(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := &opsConfig{}
	cfg.Milvus.Host = "milvus"
	cfg.Milvus.Token = "private-token-must-not-leak"
	err := setupServiceToolingFromConfigWithMilvusEntryPoint(cfg, filepath.Join(t.TempDir(), "missing"))
	if err == nil {
		t.Fatal("missing global Milvus entry point must fail tooling setup")
	}
	if strings.Contains(err.Error(), cfg.Milvus.Token) {
		t.Fatalf("tooling error exposed Milvus token: %s", err)
	}
}
