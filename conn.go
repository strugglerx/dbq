package main

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// options 统一管理命令行输入选项。
type options struct {
	conn       string
	dsn        string
	driver     string
	write      bool
	danger     bool
	tsv        bool
	json       bool
	compact    bool
	maxRows    int
	timeoutSec int
}

// Conn 是配置文件中的一个命名连接。
type Conn struct {
	Driver   string `yaml:"driver"`
	DSN      string `yaml:"dsn"`
	DSNEnv   string `yaml:"dsn_env"`
	Command  string `yaml:"command"`
	Readonly bool   `yaml:"readonly"`
}

// Config 是 .dbq.yaml 的结构。
type Config struct {
	Default     string          `yaml:"default"`
	Connections map[string]Conn `yaml:"connections"`
}

// ResolvedConn 是解析完 dsn_env / command 之后的可用连接。
type ResolvedConn struct {
	Driver   string
	DSN      string
	Readonly bool
	Source   string // 来源描述，用于 conns 命令
}

// GoFrameConfig 支持自动探测 GoFrame 项目的 config.yaml。
type GoFrameConfig struct {
	Database struct {
		Default struct {
			Link string `yaml:"link"`
			Host string `yaml:"host"`
			Port string `yaml:"port"`
			User string `yaml:"user"`
			Pass string `yaml:"pass"`
			Name string `yaml:"name"`
		} `yaml:"default"`
	} `yaml:"database"`
}

// loadConfig 读取配置文件（支持 .dbq.yaml 与 GoFrame config.yaml）。
func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// 优先尝试解析 .dbq.yaml
	var c Config
	if err := yaml.Unmarshal(data, &c); err == nil && len(c.Connections) > 0 {
		return &c, nil
	}

	// 尝试解析 GoFrame config.yaml
	var gf GoFrameConfig
	if err := yaml.Unmarshal(data, &gf); err == nil && gf.Database.Default.Link != "" {
		return &Config{
			Default: "default",
			Connections: map[string]Conn{
				"default": {
					DSN: gf.Database.Default.Link,
				},
			},
		}, nil
	}

	return nil, fmt.Errorf("解析 %s 失败或未包含有效数据库连接配置", path)
}

// findProjectConfig 从 cwd 向上查找配置文件。
func findProjectConfig() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		candidates := []string{
			dir + "/.dbq.yaml",
			dir + "/.dbq.yml",
			dir + "/manifest/config/config.yaml",
			dir + "/backend/manifest/config/config.yaml",
		}
		for _, c := range candidates {
			if st, err := os.Stat(c); err == nil && !st.IsDir() {
				return c
			}
		}
		parent := filepath_Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func filepath_Dir(p string) string {
	i := strings.LastIndexByte(p, '/')
	if i < 0 {
		return "."
	}
	if i == 0 {
		return "/"
	}
	return p[:i]
}

func userConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home + "/.config/dbq/profiles.yaml"
}

// resolveConn 按 -dsn > DBQ_DSN > -c > 项目 default 的优先级解析连接。
func resolveConn(opts options) (*ResolvedConn, error) {
	if opts.dsn != "" {
		drv := opts.driver
		dsn, d, err := normalizeDSN(opts.dsn, drv)
		if err != nil {
			return nil, err
		}
		return &ResolvedConn{Driver: d, DSN: dsn, Source: "-dsn"}, nil
	}
	if env := os.Getenv("DBQ_DSN"); env != "" {
		dsn, d, err := normalizeDSN(env, opts.driver)
		if err != nil {
			return nil, err
		}
		return &ResolvedConn{Driver: d, DSN: dsn, Source: "DBQ_DSN"}, nil
	}
	// 兼容原有 DMQ_CONFIG / DM_DSN 环境变量
	if env := os.Getenv("DM_DSN"); env != "" {
		dsn, d, err := normalizeDSN(env, "dm")
		if err != nil {
			return nil, err
		}
		return &ResolvedConn{Driver: d, DSN: dsn, Source: "DM_DSN"}, nil
	}

	// 命名连接：项目配置优先，其次用户配置。
	var projCfg, userCfg *Config
	if p := findProjectConfig(); p != "" {
		if c, err := loadConfig(p); err == nil {
			projCfg = c
		}
	}
	if up := userConfigPath(); up != "" {
		if c, err := loadConfig(up); err == nil {
			userCfg = c
		}
	}

	name := opts.conn
	if name == "" && projCfg != nil {
		name = projCfg.Default
	}
	if name == "" && userCfg != nil {
		name = userCfg.Default
	}
	if name == "" {
		return nil, fmt.Errorf("未指定连接：使用 -dsn、DBQ_DSN 或 -c <name>，或在 .dbq.yaml 中设置 default")
	}

	for _, pair := range []struct {
		cfg *Config
		src string
	}{{projCfg, "project"}, {userCfg, "user"}} {
		if pair.cfg == nil {
			continue
		}
		c, ok := pair.cfg.Connections[name]
		if !ok {
			continue
		}
		rc := &ResolvedConn{Driver: c.Driver, Readonly: c.Readonly, Source: pair.src + ":" + name}
		dsn := c.DSN
		if c.DSNEnv != "" {
			dsn = os.Getenv(c.DSNEnv)
			if dsn == "" {
				return nil, fmt.Errorf("连接 %s 的环境变量 %s 未设置", name, c.DSNEnv)
			}
		}
		if c.Command != "" {
			out, err := runCommand(c.Command, pair.src == "project" && projCfg != nil)
			if err != nil {
				return nil, fmt.Errorf("连接 %s 执行 command 失败: %w", name, err)
			}
			dsn = strings.TrimSpace(out)
		}
		if dsn == "" {
			return nil, fmt.Errorf("连接 %s 缺少 dsn/dsn_env/command", name)
		}
		d, d2, err := normalizeDSN(dsn, rc.Driver)
		if err != nil {
			return nil, err
		}
		rc.DSN, rc.Driver = d, d2
		return rc, nil
	}
	return nil, fmt.Errorf("找不到连接 %q", name)
}

// runCommand 执行 shell 命令获取 DSN。
func runCommand(cmd string, fromProject bool) (string, error) {
	c := exec_Command("sh", "-c", cmd)
	out, err := c.Output()
	return string(out), err
}

// normalizeDSN 根据内容推断驱动，并做必要的格式转换。
func normalizeDSN(dsn, driver string) (string, string, error) {
	trimmed := strings.TrimSpace(dsn)
	lower := strings.ToLower(trimmed)
	switch {
	case strings.HasPrefix(lower, "dm://"):
		return trimmed, "dm", nil
	case strings.HasPrefix(lower, "dm:"):
		d, err := dmLinkToURL(trimmed)
		return d, "dm", err
	case strings.HasPrefix(lower, "postgres://"), strings.HasPrefix(lower, "postgresql://"):
		return trimmed, "pgx", nil
	case strings.HasPrefix(lower, "pgsql:"):
		d, err := pgsqlLinkToURL(trimmed)
		return d, "pgx", err
	case strings.HasPrefix(lower, "mysql://"):
		d, err := mysqlURLToDSN(trimmed)
		return d, "mysql", err
	case strings.HasPrefix(lower, "mysql:"):
		d := strings.TrimPrefix(trimmed, "mysql:")
		return appendMySQLParams(d), "mysql", nil
	case strings.HasPrefix(lower, "sqlite:"):
		return strings.TrimPrefix(trimmed, "sqlite:"), "sqlite", nil
	case strings.Contains(trimmed, "@tcp("), strings.Contains(trimmed, "@unix("):
		return appendMySQLParams(trimmed), "mysql", nil
	case strings.HasPrefix(lower, "/"), strings.HasSuffix(lower, ".db"),
		strings.HasSuffix(lower, ".sqlite"), strings.HasSuffix(lower, ".sqlite3"), lower == ":memory:":
		return trimmed, "sqlite", nil
	}
	if driver != "" {
		return trimmed, driver, nil
	}
	return "", "", fmt.Errorf("无法从 DSN 推断驱动，请用 -driver 指定（dm/mysql/pgx/sqlite）")
}

// dmLinkToURL 把 GoFrame 达梦 link (dm:user:pass@tcp(host:port)/dbname?params) 转成 dm:// URL
func dmLinkToURL(raw string) (string, error) {
	gfRegex := regexp.MustCompile(`^dm:([^:]+):([^@]+)@(?:tcp\(([^)]+)\)|([^/]+))(?:/([^?]+))?(?:\?(.*))?$`)
	if matches := gfRegex.FindStringSubmatch(raw); len(matches) > 0 {
		user := matches[1]
		pass := matches[2]
		endpoint := matches[3]
		if endpoint == "" {
			endpoint = matches[4]
		}
		dbname := matches[5]
		extra := matches[6]

		userInfo := url.UserPassword(user, pass)
		res := fmt.Sprintf("dm://%s@%s", userInfo.String(), endpoint)
		if dbname != "" {
			res += "/" + dbname
		}
		params, _ := url.ParseQuery(extra)
		if params == nil {
			params = make(url.Values)
		}
		if dbname != "" && params.Get("schema") == "" {
			params.Set("schema", dbname)
		}
		if len(params) > 0 {
			res += "?" + params.Encode()
		}
		return res, nil
	}
	return "dm://" + strings.TrimPrefix(raw, "dm:"), nil
}

// pgsqlLinkToURL 把 GoFrame pgsql link (pgsql:user:pass@tcp(host:port)/dbname?params) 转成 postgres:// URL
func pgsqlLinkToURL(raw string) (string, error) {
	gfRegex := regexp.MustCompile(`^pgsql:([^:]+):([^@]+)@(?:tcp\(([^)]+)\)|([^/]+))(?:/([^?]+))?(?:\?(.*))?$`)
	if matches := gfRegex.FindStringSubmatch(raw); len(matches) > 0 {
		user := matches[1]
		pass := matches[2]
		endpoint := matches[3]
		if endpoint == "" {
			endpoint = matches[4]
		}
		dbname := matches[5]
		extra := matches[6]

		userInfo := url.UserPassword(user, pass)
		res := fmt.Sprintf("postgres://%s@%s/%s", userInfo.String(), endpoint, dbname)
		params, _ := url.ParseQuery(extra)
		if params == nil {
			params = make(url.Values)
		}
		if params.Get("sslmode") == "" {
			params.Set("sslmode", "disable")
		}
		if len(params) > 0 {
			res += "?" + params.Encode()
		}
		return res, nil
	}
	return "postgres://" + strings.TrimPrefix(raw, "pgsql:"), nil
}

// mysqlURLToDSN 把 mysql:// URL 转成 go-sql-driver DSN。
func mysqlURLToDSN(u string) (string, error) {
	parsed, err := url_Parse(u)
	if err != nil {
		return "", err
	}
	host := parsed.Hostname()
	port := parsed.Port()
	if port == "" {
		port = "3306"
	}
	addr := fmt.Sprintf("tcp(%s:%s)", host, port)
	user := ""
	if parsed.User != nil {
		user = parsed.User.Username()
		if pw, ok := parsed.User.Password(); ok {
			user += ":" + pw
		}
	}
	dbName := strings.TrimPrefix(parsed.Path, "/")
	dsn := user + "@" + addr + "/"
	if dbName != "" {
		dsn += dbName
	}
	return appendMySQLParams(dsn), nil
}

func appendMySQLParams(dsn string) string {
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	if !strings.Contains(dsn, "parseTime=") {
		dsn += sep + "parseTime=true&loc=Local"
	}
	return dsn
}

// maskDSN 隐藏密码，用于 conns 列表展示。
func maskDSN(dsn string) string {
	if i := strings.IndexByte(dsn, '@'); i > 0 {
		head := dsn[:i]
		if j := strings.IndexByte(head, ':'); j >= 0 {
			return head[:j] + ":***@" + dsn[i+1:]
		}
	}
	if u := strings.Index(dsn, "://"); u > 0 {
		rest := dsn[u+3:]
		if i := strings.IndexByte(rest, '@'); i > 0 {
			head := rest[:i]
			if j := strings.IndexByte(head, ':'); j >= 0 {
				return dsn[:u+3] + head[:j] + ":***@" + rest[i+1:]
			}
		}
	}
	return dsn
}
