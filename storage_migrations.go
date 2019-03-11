package main

type storageMigration struct {
	From SemVer
	To   SemVer
	Do   func(*Storage) error
}

// Keep the migrations sorted from the lowest to highest version, the migration logic doesn't check.
var storageMigrations = []storageMigration{}
