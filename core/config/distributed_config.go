package config

// DistributedConfig holds configuration for horizontal scaling mode.
// When Enabled is true, PostgreSQL and NATS are required.
type DistributedConfig struct {
	Enabled           bool   // --distributed / LOCALAI_DISTRIBUTED
	InstanceID        string // --instance-id / LOCALAI_INSTANCE_ID (auto-generated UUID if empty)
	NatsURL           string // --nats-url / LOCALAI_NATS_URL
	StorageURL        string // --storage-url / LOCALAI_STORAGE_URL (S3 endpoint)
	RegistrationToken string // --registration-token / LOCALAI_REGISTRATION_TOKEN (required token for node registration)
	AutoApproveNodes  bool   // --auto-approve-nodes / LOCALAI_AUTO_APPROVE_NODES (skip admin approval for new workers)

	// S3 configuration (used when StorageURL is set)
	StorageBucket    string // --storage-bucket / LOCALAI_STORAGE_BUCKET
	StorageRegion    string // --storage-region / LOCALAI_STORAGE_REGION
	StorageAccessKey string // --storage-access-key / LOCALAI_STORAGE_ACCESS_KEY
	StorageSecretKey string // --storage-secret-key / LOCALAI_STORAGE_SECRET_KEY
}

// Distributed config options

var EnableDistributed = func(o *ApplicationConfig) {
	o.Distributed.Enabled = true
}

func WithDistributedInstanceID(id string) AppOption {
	return func(o *ApplicationConfig) {
		o.Distributed.InstanceID = id
	}
}

func WithNatsURL(url string) AppOption {
	return func(o *ApplicationConfig) {
		o.Distributed.NatsURL = url
	}
}

func WithRegistrationToken(token string) AppOption {
	return func(o *ApplicationConfig) {
		o.Distributed.RegistrationToken = token
	}
}

func WithStorageURL(url string) AppOption {
	return func(o *ApplicationConfig) {
		o.Distributed.StorageURL = url
	}
}

func WithStorageBucket(bucket string) AppOption {
	return func(o *ApplicationConfig) {
		o.Distributed.StorageBucket = bucket
	}
}

func WithStorageRegion(region string) AppOption {
	return func(o *ApplicationConfig) {
		o.Distributed.StorageRegion = region
	}
}

func WithStorageAccessKey(key string) AppOption {
	return func(o *ApplicationConfig) {
		o.Distributed.StorageAccessKey = key
	}
}

func WithStorageSecretKey(key string) AppOption {
	return func(o *ApplicationConfig) {
		o.Distributed.StorageSecretKey = key
	}
}

var EnableAutoApproveNodes = func(o *ApplicationConfig) {
	o.Distributed.AutoApproveNodes = true
}
