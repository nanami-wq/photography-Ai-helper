package mysql

import (
	"errors"
	"fmt"
	"time"

	"go-service-starter/config"
	"go-service-starter/domain"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

var (
	ErrNoHost     = errors.New("mysql host is empty")
	ErrNoPort     = errors.New("mysql port is empty")
	ErrNoUsername = errors.New("mysql username is empty")
	ErrNoPassword = errors.New("mysql password is empty")
	ErrNoDatabase = errors.New("mysql database is empty")
)

type MySQLConf struct {
	Host     string
	Port     string
	Username string
	Password string
	Database string
}

type Orm struct {
	Host     string
	Port     string
	Username string
	Password string
	Database string
	Conf     *gorm.Config
	*gorm.DB
}

type Option func(m *Orm)

func New(conf config.MySQLConfig, opts ...Option) (*gorm.DB, error) {
	orm, err := NewMysqlOrm(&MySQLConf{
		Host:     conf.Host,
		Port:     conf.Port,
		Username: conf.Username,
		Password: conf.Password,
		Database: conf.Database,
	}, opts...)
	if err != nil {
		return nil, err
	}
	return orm.DB, nil
}

func InitMySQL() (*gorm.DB, error) {
	db, err := New(config.GetConfig().MySQL)
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(50)
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	sqlDB.SetConnMaxIdleTime(10 * time.Minute)
	if err := sqlDB.Ping(); err != nil {
		return nil, errors.New("mysql ping failed: " + err.Error())
	}
	if err := AutoMigrateAll(db); err != nil {
		return nil, errors.New("mysql automigrate failed: " + err.Error())
	}
	return db, nil
}

func (c *MySQLConf) Validate() error {
	if c.Host == "" {
		return ErrNoHost
	}
	if c.Port == "" {
		return ErrNoPort
	}
	if c.Username == "" {
		return ErrNoUsername
	}
	if c.Password == "" {
		return ErrNoPassword
	}
	if c.Database == "" {
		return ErrNoDatabase
	}
	return nil
}

func WithConf(conf *gorm.Config) Option {
	return func(m *Orm) {
		m.Conf = conf
	}
}

func WithIp(host, port string) Option {
	return func(m *Orm) {
		m.Host = host
		m.Port = port
	}
}

func WithUP(username, password string) Option {
	return func(m *Orm) {
		m.Username = username
		m.Password = password
	}
}

func WithDB(database string) Option {
	return func(m *Orm) {
		m.Database = database
	}
}

func NewMysqlOrm(conf *MySQLConf, opts ...Option) (*Orm, error) {
	if err := conf.Validate(); err != nil {
		return nil, err
	}
	opts = append([]Option{WithDB(conf.Database)}, opts...)
	opts = append([]Option{WithUP(conf.Username, conf.Password)}, opts...)
	opts = append([]Option{WithIp(conf.Host, conf.Port)}, opts...)
	m, err := newOrm(opts...)
	if err != nil {
		return nil, err
	}
	return m, nil
}

func newOrm(opts ...Option) (*Orm, error) {
	m := &Orm{}
	for _, opt := range opts {
		opt(m)
	}
	conf := m.Conf
	if conf == nil {
		conf = &gorm.Config{}
	}
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		m.Username, m.Password, m.Host, m.Port, m.Database)
	db, err := gorm.Open(mysql.Open(dsn), conf)
	if err != nil {
		return nil, err
	}
	m.DB = db
	return m, nil
}

func AutoMigrateAll(db any) error {
	gdb, ok := db.(interface {
		AutoMigrate(dst ...any) error
	})
	if !ok {
		return errors.New("invalid gorm db")
	}
	return gdb.AutoMigrate(&domain.User{})
}
