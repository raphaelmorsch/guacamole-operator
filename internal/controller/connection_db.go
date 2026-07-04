package controller

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	guacamolev1alpha1 "github.com/raphaelmorsch/guacamole-operator/api/v1alpha1"
)

type mysqlCredentials struct {
	Host     string
	Port     string
	User     string
	Password string
	Database string
}

func (r *GuacamoleConnectionReconciler) resolveMySQLCredentials(
	ctx context.Context,
	guac *guacamolev1alpha1.Guacamole,
) (mysqlCredentials, error) {
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      mysqlSecretName(guac.Name),
		Namespace: guac.Namespace,
	}, secret); err != nil {
		return mysqlCredentials{}, fmt.Errorf("get mysql secret: %w", err)
	}

	user := string(secret.Data["database-user"])
	password := string(secret.Data["database-password"])
	database := string(secret.Data["database-name"])
	if user == "" || password == "" || database == "" {
		return mysqlCredentials{}, fmt.Errorf("mysql secret %s is missing database credentials", secret.Name)
	}

	return mysqlCredentials{
		Host:     serviceFQDN(mysqlServiceName(guac.Name), guac.Namespace),
		Port:     "3306",
		User:     user,
		Password: password,
		Database: database,
	}, nil
}

func openMySQL(creds mysqlCredentials) (*sql.DB, error) {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8&parseTime=true",
		creds.User, creds.Password, creds.Host, creds.Port, creds.Database)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(3)
	db.SetMaxIdleConns(1)
	return db, nil
}

func (r *GuacamoleConnectionReconciler) waitForDatabase(ctx context.Context, db *sql.DB) error {
	var exists int
	err := db.QueryRowContext(ctx, "SELECT 1 FROM guacamole_user LIMIT 1").Scan(&exists)
	if err != nil {
		return fmt.Errorf("guacamole schema is not ready: %w", err)
	}
	return nil
}

func connectionDisplayName(conn *guacamolev1alpha1.GuacamoleConnection) string {
	if conn.Spec.DisplayName != "" {
		return conn.Spec.DisplayName
	}
	return conn.Name
}

func (r *GuacamoleConnectionReconciler) resolveSecretValue(
	ctx context.Context,
	namespace string,
	ref *guacamolev1alpha1.SecretKeyRef,
) (string, error) {
	if ref == nil {
		return "", nil
	}
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: ref.Name, Namespace: namespace}, secret); err != nil {
		return "", fmt.Errorf("get secret %s: %w", ref.Name, err)
	}
	key := ref.Key
	if key == "" {
		key = "password"
	}
	value, ok := secret.Data[key]
	if !ok {
		return "", fmt.Errorf("secret %s does not contain key %s", ref.Name, key)
	}
	return string(value), nil
}

func (r *GuacamoleConnectionReconciler) buildConnectionParameters(
	ctx context.Context,
	conn *guacamolev1alpha1.GuacamoleConnection,
) (map[string]string, error) {
	params := make(map[string]string)
	for key, value := range conn.Spec.AdditionalParameters {
		if value != "" {
			params[key] = value
		}
	}

	switch conn.Spec.Protocol {
	case "rdp":
		if conn.Spec.RDP == nil {
			return nil, fmt.Errorf("spec.rdp is required when protocol is rdp")
		}
		if err := appendRDPParameters(ctx, r, conn.Namespace, conn.Spec.RDP, params); err != nil {
			return nil, err
		}
	case "vnc":
		if conn.Spec.VNC == nil {
			return nil, fmt.Errorf("spec.vnc is required when protocol is vnc")
		}
		if err := appendVNCParameters(ctx, r, conn.Namespace, conn.Spec.VNC, params); err != nil {
			return nil, err
		}
	case "ssh":
		if conn.Spec.SSH == nil {
			return nil, fmt.Errorf("spec.ssh is required when protocol is ssh")
		}
		if err := appendSSHParameters(ctx, r, conn.Namespace, conn.Spec.SSH, params); err != nil {
			return nil, err
		}
	case "telnet", "kubernetes":
		if len(params) == 0 {
			return nil, fmt.Errorf("spec.additionalParameters must include protocol parameters for %s", conn.Spec.Protocol)
		}
	default:
		return nil, fmt.Errorf("unsupported protocol %q", conn.Spec.Protocol)
	}

	if len(params) == 0 {
		return nil, fmt.Errorf("no connection parameters resolved")
	}
	return params, nil
}

func appendRDPParameters(
	ctx context.Context,
	r *GuacamoleConnectionReconciler,
	namespace string,
	rdp *guacamolev1alpha1.RDPConnectionSpec,
	params map[string]string,
) error {
	if rdp.Hostname == "" {
		return fmt.Errorf("spec.rdp.hostname is required")
	}

	setParam(params, "hostname", rdp.Hostname)
	setParamInt(params, "port", rdp.Port, 3389)
	setParam(params, "username", rdp.Username)
	setParam(params, "domain", rdp.Domain)
	setParam(params, "security", valueOrDefault(rdp.Security, "nla"))
	setParamBool(params, "ignore-cert", rdp.IgnoreCert)
	setParamInt(params, "timeout", rdp.Timeout, 0)
	setParamInt(params, "width", rdp.Width, 0)
	setParamInt(params, "height", rdp.Height, 0)
	setParamInt(params, "dpi", rdp.Dpi, 0)
	setParamInt(params, "color-depth", rdp.ColorDepth, 0)
	setParam(params, "resize-method", rdp.ResizeMethod)
	setParam(params, "server-layout", rdp.ServerLayout)
	setParamBool(params, "console", rdp.Console)
	setParam(params, "initial-program", rdp.InitialProgram)
	setParam(params, "client-name", rdp.ClientName)
	setParam(params, "timezone", rdp.Timezone)
	setParamBool(params, "enable-drive", rdp.EnableDrive)
	setParam(params, "drive-name", rdp.DriveName)
	setParam(params, "drive-path", rdp.DrivePath)
	setParamBool(params, "create-drive-path", rdp.CreateDrivePath)
	setParamBool(params, "enable-printing", rdp.EnablePrinting)
	setParam(params, "printer-name", rdp.PrinterName)
	setParamBool(params, "disable-audio", rdp.DisableAudio)
	setParamBool(params, "enable-audio-input", rdp.EnableAudioInput)
	setParam(params, "gateway-hostname", rdp.GatewayHostname)
	setParamInt(params, "gateway-port", rdp.GatewayPort, 0)
	setParam(params, "gateway-username", rdp.GatewayUsername)
	setParam(params, "gateway-domain", rdp.GatewayDomain)
	setParam(params, "remote-app", rdp.RemoteApp)
	setParam(params, "remote-app-dir", rdp.RemoteAppDir)
	setParam(params, "remote-app-args", rdp.RemoteAppArgs)
	setParamBool(params, "enable-sftp", rdp.EnableSFTP)
	setParamBool(params, "enable-wallpaper", rdp.EnableWallpaper)
	setParamBool(params, "enable-theming", rdp.EnableTheming)
	setParamBool(params, "enable-font-smoothing", rdp.EnableFontSmoothing)
	setParamBool(params, "disable-bitmap-caching", rdp.DisableBitmapCaching)
	setParamBool(params, "disable-offscreen-caching", rdp.DisableOffscreenCaching)
	setParamBool(params, "disable-glyph-caching", rdp.DisableGlyphCaching)

	password := rdp.Password
	if password == "" && rdp.PasswordSecretRef != nil {
		value, err := r.resolveSecretValue(ctx, namespace, rdp.PasswordSecretRef)
		if err != nil {
			return err
		}
		password = value
	}
	setParam(params, "password", password)

	gatewayPassword := rdp.GatewayPassword
	if gatewayPassword == "" && rdp.GatewayPasswordSecretRef != nil {
		value, err := r.resolveSecretValue(ctx, namespace, rdp.GatewayPasswordSecretRef)
		if err != nil {
			return err
		}
		gatewayPassword = value
	}
	setParam(params, "gateway-password", gatewayPassword)
	return nil
}

func appendVNCParameters(
	ctx context.Context,
	r *GuacamoleConnectionReconciler,
	namespace string,
	vnc *guacamolev1alpha1.VNCConnectionSpec,
	params map[string]string,
) error {
	if vnc.Hostname == "" {
		return fmt.Errorf("spec.vnc.hostname is required")
	}

	setParam(params, "hostname", vnc.Hostname)
	setParamInt(params, "port", vnc.Port, 5900)
	setParamBool(params, "read-only", vnc.ReadOnly)
	setParamBool(params, "swap-red-blue", vnc.SwapRedBlue)
	setParam(params, "cursor", vnc.Cursor)
	setParamInt(params, "color-depth", vnc.ColorDepth, 0)
	setParamInt(params, "width", vnc.Width, 0)
	setParamInt(params, "height", vnc.Height, 0)
	setParamInt(params, "dpi", vnc.Dpi, 0)

	password := vnc.Password
	if password == "" && vnc.PasswordSecretRef != nil {
		value, err := r.resolveSecretValue(ctx, namespace, vnc.PasswordSecretRef)
		if err != nil {
			return err
		}
		password = value
	}
	setParam(params, "password", password)
	return nil
}

func appendSSHParameters(
	ctx context.Context,
	r *GuacamoleConnectionReconciler,
	namespace string,
	sshSpec *guacamolev1alpha1.SSHConnectionSpec,
	params map[string]string,
) error {
	if sshSpec.Hostname == "" {
		return fmt.Errorf("spec.ssh.hostname is required")
	}

	setParam(params, "hostname", sshSpec.Hostname)
	setParamInt(params, "port", sshSpec.Port, 22)
	setParam(params, "username", sshSpec.Username)
	setParam(params, "command", sshSpec.Command)
	setParam(params, "color-scheme", sshSpec.ColorScheme)
	setParam(params, "font-name", sshSpec.FontName)
	setParamInt(params, "font-size", sshSpec.FontSize, 0)

	password := sshSpec.Password
	if password == "" && sshSpec.PasswordSecretRef != nil {
		value, err := r.resolveSecretValue(ctx, namespace, sshSpec.PasswordSecretRef)
		if err != nil {
			return err
		}
		password = value
	}
	setParam(params, "password", password)

	privateKey := sshSpec.PrivateKey
	if privateKey == "" && sshSpec.PrivateKeySecretRef != nil {
		value, err := r.resolveSecretValue(ctx, namespace, sshSpec.PrivateKeySecretRef)
		if err != nil {
			return err
		}
		privateKey = value
	}
	setParam(params, "private-key", privateKey)

	passphrase := sshSpec.Passphrase
	if passphrase == "" && sshSpec.PassphraseSecretRef != nil {
		value, err := r.resolveSecretValue(ctx, namespace, sshSpec.PassphraseSecretRef)
		if err != nil {
			return err
		}
		passphrase = value
	}
	setParam(params, "passphrase", passphrase)
	return nil
}

func setParam(params map[string]string, name, value string) {
	if value != "" {
		params[name] = value
	}
}

func setParamInt(params map[string]string, name string, value *int32, defaultValue int32) {
	if value != nil {
		params[name] = strconv.FormatInt(int64(*value), 10)
		return
	}
	if defaultValue > 0 {
		params[name] = strconv.FormatInt(int64(defaultValue), 10)
	}
}

func setParamBool(params map[string]string, name string, value *bool) {
	if value == nil {
		return
	}
	if *value {
		params[name] = "true"
	} else {
		params[name] = "false"
	}
}

type desiredConnection struct {
	ConnectionID          int64
	ConnectionName        string
	Protocol              string
	ParentGroupID         sql.NullInt64
	MaxConnections        sql.NullInt32
	MaxConnectionsPerUser sql.NullInt32
	ProxyHostname         sql.NullString
	ProxyPort             sql.NullInt32
	ProxyEncryption       sql.NullString
	Parameters            map[string]string
	Permissions           []guacamolev1alpha1.ConnectionPermissionSpec
}

func lookupParentGroupID(ctx context.Context, db *sql.DB, groupName string) (sql.NullInt64, error) {
	if groupName == "" {
		return sql.NullInt64{}, nil
	}
	var groupID int64
	err := db.QueryRowContext(ctx,
		"SELECT connection_group_id FROM guacamole_connection_group WHERE connection_group_name = ? AND parent_id IS NULL",
		groupName,
	).Scan(&groupID)
	if err == sql.ErrNoRows {
		return sql.NullInt64{}, fmt.Errorf("connection group %q not found", groupName)
	}
	if err != nil {
		return sql.NullInt64{}, err
	}
	return sql.NullInt64{Int64: groupID, Valid: true}, nil
}

func (r *GuacamoleConnectionReconciler) upsertConnection(
	ctx context.Context,
	db *sql.DB,
	conn *guacamolev1alpha1.GuacamoleConnection,
) (int64, error) {
	params, err := r.buildConnectionParameters(ctx, conn)
	if err != nil {
		return 0, err
	}

	parentGroupID, err := lookupParentGroupID(ctx, db, conn.Spec.ParentGroup)
	if err != nil {
		return 0, err
	}

	desired := desiredConnection{
		ConnectionID:   conn.Status.ConnectionID,
		ConnectionName: connectionDisplayName(conn),
		Protocol:       conn.Spec.Protocol,
		ParentGroupID:  parentGroupID,
		Parameters:     params,
		Permissions:    conn.Spec.Permissions,
	}
	if conn.Spec.MaxConnections != nil {
		desired.MaxConnections = sql.NullInt32{Int32: *conn.Spec.MaxConnections, Valid: true}
	}
	if conn.Spec.MaxConnectionsPerUser != nil {
		desired.MaxConnectionsPerUser = sql.NullInt32{Int32: *conn.Spec.MaxConnectionsPerUser, Valid: true}
	}
	if conn.Spec.Proxy != nil {
		if conn.Spec.Proxy.Hostname != "" {
			desired.ProxyHostname = sql.NullString{String: conn.Spec.Proxy.Hostname, Valid: true}
		}
		if conn.Spec.Proxy.Port != nil {
			desired.ProxyPort = sql.NullInt32{Int32: *conn.Spec.Proxy.Port, Valid: true}
		}
		if conn.Spec.Proxy.Encryption != "" {
			desired.ProxyEncryption = sql.NullString{String: conn.Spec.Proxy.Encryption, Valid: true}
		}
	}

	connectionID, err := ensureConnectionRow(ctx, db, desired)
	if err != nil {
		return 0, err
	}
	if err := syncConnectionParameters(ctx, db, connectionID, desired.Parameters); err != nil {
		return 0, err
	}
	if err := syncConnectionPermissions(ctx, db, connectionID, desired.Permissions); err != nil {
		return 0, err
	}
	return connectionID, nil
}

func ensureConnectionRow(ctx context.Context, db *sql.DB, desired desiredConnection) (int64, error) {
	if desired.ConnectionID > 0 {
		_, err := db.ExecContext(ctx, `
UPDATE guacamole_connection
SET connection_name = ?, protocol = ?, parent_id = ?, max_connections = ?, max_connections_per_user = ?,
    proxy_hostname = ?, proxy_port = ?, proxy_encryption_method = ?
WHERE connection_id = ?`,
			desired.ConnectionName,
			desired.Protocol,
			desired.ParentGroupID,
			desired.MaxConnections,
			desired.MaxConnectionsPerUser,
			desired.ProxyHostname,
			desired.ProxyPort,
			desired.ProxyEncryption,
			desired.ConnectionID,
		)
		if err != nil {
			return 0, err
		}
		return desired.ConnectionID, nil
	}

	var connectionID int64
	if desired.ParentGroupID.Valid {
		err := db.QueryRowContext(ctx,
			"SELECT connection_id FROM guacamole_connection WHERE connection_name = ? AND parent_id = ?",
			desired.ConnectionName, desired.ParentGroupID.Int64,
		).Scan(&connectionID)
		if err != nil && err != sql.ErrNoRows {
			return 0, err
		}
		if err == nil {
			_, err = db.ExecContext(ctx, `
UPDATE guacamole_connection
SET protocol = ?, max_connections = ?, max_connections_per_user = ?,
    proxy_hostname = ?, proxy_port = ?, proxy_encryption_method = ?
WHERE connection_id = ?`,
				desired.Protocol,
				desired.MaxConnections,
				desired.MaxConnectionsPerUser,
				desired.ProxyHostname,
				desired.ProxyPort,
				desired.ProxyEncryption,
				connectionID,
			)
			return connectionID, err
		}
	} else {
		err := db.QueryRowContext(ctx,
			"SELECT connection_id FROM guacamole_connection WHERE connection_name = ? AND parent_id IS NULL",
			desired.ConnectionName,
		).Scan(&connectionID)
		if err != nil && err != sql.ErrNoRows {
			return 0, err
		}
		if err == nil {
			_, err = db.ExecContext(ctx, `
UPDATE guacamole_connection
SET protocol = ?, max_connections = ?, max_connections_per_user = ?,
    proxy_hostname = ?, proxy_port = ?, proxy_encryption_method = ?
WHERE connection_id = ?`,
				desired.Protocol,
				desired.MaxConnections,
				desired.MaxConnectionsPerUser,
				desired.ProxyHostname,
				desired.ProxyPort,
				desired.ProxyEncryption,
				connectionID,
			)
			return connectionID, err
		}
	}

	result, err := db.ExecContext(ctx, `
INSERT INTO guacamole_connection
  (connection_name, protocol, parent_id, max_connections, max_connections_per_user, proxy_hostname, proxy_port, proxy_encryption_method)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		desired.ConnectionName,
		desired.Protocol,
		desired.ParentGroupID,
		desired.MaxConnections,
		desired.MaxConnectionsPerUser,
		desired.ProxyHostname,
		desired.ProxyPort,
		desired.ProxyEncryption,
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func syncConnectionParameters(ctx context.Context, db *sql.DB, connectionID int64, params map[string]string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, "DELETE FROM guacamole_connection_parameter WHERE connection_id = ?", connectionID); err != nil {
		return err
	}
	for name, value := range params {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO guacamole_connection_parameter (connection_id, parameter_name, parameter_value) VALUES (?, ?, ?)",
			connectionID, name, value,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func syncConnectionPermissions(
	ctx context.Context,
	db *sql.DB,
	connectionID int64,
	permissions []guacamolev1alpha1.ConnectionPermissionSpec,
) error {
	if len(permissions) == 0 {
		return nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, "DELETE FROM guacamole_connection_permission WHERE connection_id = ?", connectionID); err != nil {
		return err
	}

	for _, permission := range permissions {
		entityType := valueOrDefault(permission.EntityType, "USER")
		perm := valueOrDefault(permission.Permission, "READ")
		var entityID int64
		err := tx.QueryRowContext(ctx,
			"SELECT entity_id FROM guacamole_entity WHERE name = ? AND type = ?",
			permission.EntityName, entityType,
		).Scan(&entityID)
		if err == sql.ErrNoRows {
			return fmt.Errorf("guacamole entity %q (%s) not found", permission.EntityName, entityType)
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO guacamole_connection_permission (entity_id, connection_id, permission) VALUES (?, ?, ?)",
			entityID, connectionID, perm,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func deleteConnection(ctx context.Context, db *sql.DB, connectionID int64) error {
	if connectionID <= 0 {
		return nil
	}
	_, err := db.ExecContext(ctx, "DELETE FROM guacamole_connection WHERE connection_id = ?", connectionID)
	return err
}

func guacamoleInstanceNamespace(conn *guacamolev1alpha1.GuacamoleConnection) string {
	if conn.Spec.GuacamoleRef.Namespace != "" {
		return conn.Spec.GuacamoleRef.Namespace
	}
	return conn.Namespace
}

func validateConnectionSpec(conn *guacamolev1alpha1.GuacamoleConnection) error {
	if conn.Spec.GuacamoleRef.Name == "" {
		return fmt.Errorf("spec.guacamoleRef.name is required")
	}
	if conn.Spec.Protocol == "" {
		return fmt.Errorf("spec.protocol is required")
	}
	switch conn.Spec.Protocol {
	case "rdp":
		if conn.Spec.RDP == nil || conn.Spec.RDP.Hostname == "" {
			return fmt.Errorf("spec.rdp.hostname is required for rdp connections")
		}
	case "vnc":
		if conn.Spec.VNC == nil || conn.Spec.VNC.Hostname == "" {
			return fmt.Errorf("spec.vnc.hostname is required for vnc connections")
		}
	case "ssh":
		if conn.Spec.SSH == nil || conn.Spec.SSH.Hostname == "" {
			return fmt.Errorf("spec.ssh.hostname is required for ssh connections")
		}
	case "telnet", "kubernetes":
		if len(conn.Spec.AdditionalParameters) == 0 {
			return fmt.Errorf("spec.additionalParameters is required for protocol %s", conn.Spec.Protocol)
		}
	default:
		return fmt.Errorf("unsupported protocol %q", conn.Spec.Protocol)
	}

	for _, permission := range conn.Spec.Permissions {
		if strings.TrimSpace(permission.EntityName) == "" {
			return fmt.Errorf("permissions.entityName is required")
		}
	}
	return nil
}
