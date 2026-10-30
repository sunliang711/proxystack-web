package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/eagle/proxystack-web/internal/domain"
	"github.com/eagle/proxystack-web/internal/domain/validation"
)

// 本文件按参考项目的 YAML 布局（config.yaml + stacks/*.yaml）加载 golden 测试 fixture，
// 只供测试使用：生产环境的配置来源是数据库。

// LoadConfig 读取 fixture 全局配置。
func LoadConfig(path string) (domain.GlobalConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return domain.GlobalConfig{}, fmt.Errorf("Config file could not be read: %s (%w)", path, err)
	}
	var config domain.GlobalConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return domain.GlobalConfig{}, fmt.Errorf("Config file contains invalid YAML: %s\n%w", path, err)
	}
	config.BaseDir = filepath.Dir(path)
	if err := config.Validate(); err != nil {
		return domain.GlobalConfig{}, err
	}
	config.ConfigPath = path
	return config, nil
}

// LoadStack 读取单个 fixture stack 并执行文件内校验。
func LoadStack(path string) (domain.Stack, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return domain.Stack{}, fmt.Errorf("Stack file could not be read: %s (%w)", path, err)
	}
	var stack domain.Stack
	if err := yaml.Unmarshal(data, &stack); err != nil {
		return domain.Stack{}, fmt.Errorf("Stack file contains invalid YAML: %s\n%w", path, err)
	}
	stack.SourcePath = path
	if err := stack.Validate(); err != nil {
		return domain.Stack{}, err
	}
	expectedName := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if expectedName != stack.Name {
		return domain.Stack{}, validation.ConfigValidationError{
			Issues: []validation.Issue{{
				Path:    fmt.Sprintf("stacks.%s.name", filepath.Base(path)),
				Message: fmt.Sprintf("stack name must match file name: %s", expectedName),
			}},
		}
	}
	return stack, nil
}

// LoadStacks 读取 fixture 目录下全部 stack 并执行跨 stack 校验。
func LoadStacks(config domain.GlobalConfig, checkSystemPorts bool) (domain.StackSet, error) {
	stackPaths, err := filepath.Glob(filepath.Join(config.StacksDir(), "*.yaml"))
	if err != nil {
		return domain.StackSet{}, fmt.Errorf("invalid stacks glob: %w", err)
	}
	sort.Strings(stackPaths)
	stacks := make([]domain.Stack, 0, len(stackPaths))
	for _, stackPath := range stackPaths {
		stack, err := LoadStack(stackPath)
		if err != nil {
			return domain.StackSet{}, err
		}
		stacks = append(stacks, stack)
	}
	stackSet, err := domain.ResolveStackSetUserRefs(domain.StackSet{Config: config, Stacks: stacks})
	if err != nil {
		return domain.StackSet{}, err
	}
	var options []validation.Option
	if !checkSystemPorts {
		options = append(options, validation.WithPortChecker(validation.NoopPortChecker{}))
	}
	if err := validation.ValidateStackSet(stackSet, options...); err != nil {
		return domain.StackSet{}, err
	}
	return stackSet, nil
}
