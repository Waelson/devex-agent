package errors

// ErrorCode is a stable, machine-readable error identifier.
type ErrorCode string

const (
	// Configuration
	CodeConfigInvalid ErrorCode = "CONFIG_INVALID"

	// Platform API
	CodePlatformAPIUnavailable ErrorCode = "PLATFORM_API_UNAVAILABLE"
	CodePlatformAPIError       ErrorCode = "PLATFORM_API_ERROR"
	CodeAuthenticationFailed   ErrorCode = "AUTHENTICATION_FAILED"
	CodeAuthorizationFailed    ErrorCode = "AUTHORIZATION_FAILED"
	CodeCommandClaimFailed     ErrorCode = "COMMAND_CLAIM_FAILED"
	CodeCommandInvalid         ErrorCode = "COMMAND_INVALID"
	CodeCommandTimeout         ErrorCode = "COMMAND_TIMEOUT"

	// Docker
	CodeDockerUnavailable      ErrorCode = "DOCKER_UNAVAILABLE"
	CodeDockerCommandTimeout   ErrorCode = "DOCKER_COMMAND_TIMEOUT"
	CodeImagePullFailed        ErrorCode = "IMAGE_PULL_FAILED"
	CodeImageNotFound          ErrorCode = "IMAGE_NOT_FOUND"
	CodeContainerAlreadyExists ErrorCode = "CONTAINER_ALREADY_EXISTS"
	CodeContainerStartFailed   ErrorCode = "CONTAINER_START_FAILED"
	CodeContainerStopFailed    ErrorCode = "CONTAINER_STOP_FAILED"
	CodeContainerRemoveFailed  ErrorCode = "CONTAINER_REMOVE_FAILED"
	CodeContainerInspectFailed ErrorCode = "CONTAINER_INSPECT_FAILED"
	CodeContainerListFailed    ErrorCode = "CONTAINER_LIST_FAILED"
	CodeContainerNotFound      ErrorCode = "CONTAINER_NOT_FOUND"
	CodeContainerNotManaged    ErrorCode = "CONTAINER_NOT_MANAGED"

	// Port management
	CodePortAllocationFailed  ErrorCode = "PORT_ALLOCATION_FAILED"
	CodePortRangeExhausted    ErrorCode = "PORT_RANGE_EXHAUSTED"
	CodePortAlreadyReserved   ErrorCode = "PORT_ALREADY_RESERVED"
	CodePortAlreadyInUse      ErrorCode = "PORT_ALREADY_IN_USE"
	CodePortStateInconsistent ErrorCode = "PORT_STATE_INCONSISTENT"
	CodePortReleaseFailed     ErrorCode = "PORT_RELEASE_FAILED"
	CodePortLockFailed        ErrorCode = "PORT_LOCK_FAILED"

	// Health checks
	CodeHealthCheckFailed              ErrorCode = "HEALTH_CHECK_FAILED"
	CodeHealthCheckTimeout             ErrorCode = "HEALTH_CHECK_TIMEOUT"
	CodeHealthCheckConnectionRefused   ErrorCode = "HEALTH_CHECK_CONNECTION_REFUSED"
	CodeHealthCheckInvalidResponse     ErrorCode = "HEALTH_CHECK_INVALID_RESPONSE"
	CodeHealthCheckUnexpectedStatus    ErrorCode = "HEALTH_CHECK_UNEXPECTED_STATUS"
	CodeHealthCheckContainerNotRunning ErrorCode = "HEALTH_CHECK_CONTAINER_NOT_RUNNING"
	CodeHealthCheckGatewayRouteFailed  ErrorCode = "HEALTH_CHECK_GATEWAY_ROUTE_FAILED"

	// Caddy / Gateway
	CodeCaddyAdminUnavailable       ErrorCode = "CADDY_ADMIN_UNAVAILABLE"
	CodeCaddyConfigGenerationFailed ErrorCode = "CADDY_CONFIG_GENERATION_FAILED"
	CodeCaddyConfigInvalid          ErrorCode = "CADDY_CONFIG_INVALID"
	CodeCaddyLoadFailed             ErrorCode = "CADDY_LOAD_FAILED"
	CodeCaddyRouteValidationFailed  ErrorCode = "CADDY_ROUTE_VALIDATION_FAILED"
	CodeCaddyLastGoodRestoreFailed  ErrorCode = "CADDY_LAST_GOOD_RESTORE_FAILED"
	CodeDesiredStateFetchFailed     ErrorCode = "DESIRED_STATE_FETCH_FAILED"
	CodeInvalidHost                 ErrorCode = "INVALID_HOST"
	CodeInvalidUpstream             ErrorCode = "INVALID_UPSTREAM"

	// Local state
	CodeStateStoreFailed          ErrorCode = "STATE_STORE_FAILED"
	CodeStateLoadFailed           ErrorCode = "STATE_LOAD_FAILED"
	CodeStateWriteFailed          ErrorCode = "STATE_WRITE_FAILED"
	CodeStateCorrupted            ErrorCode = "STATE_CORRUPTED"
	CodeStateSchemaUnsupported    ErrorCode = "STATE_SCHEMA_UNSUPPORTED"
	CodeStateReconciliationFailed ErrorCode = "STATE_RECONCILIATION_FAILED"
	CodeLockAcquireFailed         ErrorCode = "LOCK_ACQUIRE_FAILED"
	CodeLockReleaseFailed         ErrorCode = "LOCK_RELEASE_FAILED"

	// Reconciliation
	CodeReconciliationFailed ErrorCode = "RECONCILIATION_FAILED"
)
