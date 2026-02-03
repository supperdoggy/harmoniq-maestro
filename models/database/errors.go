package database

import "errors"

var (
	ErrNotSure             = errors.New("please be sure what you are doing")
	ErrEmptyCollectionName = errors.New("collection name cannot be empty")
	ErrEmptyDBName         = errors.New("database name cannot be empty")
)
