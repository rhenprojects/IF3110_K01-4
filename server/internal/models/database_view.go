package models

type DatabaseItem struct {
	ID     string      `json:"id"`
	Name   string      `json:"name"`
	DBName string      `json:"db_name"`
	Level  AccessLevel `json:"level"`
}

type DatabaseListResponse struct {
	Databases []DatabaseItem `json:"databases"`
}

type SchemaColumn struct {
	Name         string  `json:"name"`
	Type         string  `json:"type"`
	Nullable     bool    `json:"nullable"`
	Default      *string `json:"default"`
	IsPrimaryKey bool    `json:"is_primary_key"`
}

type SchemaConstraint struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Definition string `json:"definition"`
}

type SchemaIndex struct {
	Name       string `json:"name"`
	IsUnique   bool   `json:"is_unique"`
	Definition string `json:"definition"`
}

type SchemaTable struct {
	Name        string             `json:"name"`
	Type        string             `json:"type"`
	Columns     []SchemaColumn     `json:"columns"`
	Constraints []SchemaConstraint `json:"constraints"`
	Indexes     []SchemaIndex      `json:"indexes"`
}

type SchemaInfo struct {
	Name   string        `json:"name"`
	Tables []SchemaTable `json:"tables"`
}

type SchemaResponse struct {
	Schemas []SchemaInfo `json:"schemas"`
}

type RowColumn struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type RowsResponse struct {
	Columns []RowColumn `json:"columns"`
	Rows    [][]any     `json:"rows"`
	Total   int64       `json:"total"`
	Limit   int         `json:"limit"`
	Offset  int         `json:"offset"`
}

type AccessEntry struct {
	ConnectionID string      `json:"connection_id"`
	Level        AccessLevel `json:"level"`
}

type GroupAccessResponse struct {
	Access []AccessEntry `json:"access"`
}

type SetGroupMembersRequest struct {
	UserIDs []string `json:"user_ids"`
}

type SetGroupAccessListRequest struct {
	Access []AccessEntry `json:"access"`
}

type MigrationListResponse struct {
	Migrations []Migration `json:"migrations"`
}
