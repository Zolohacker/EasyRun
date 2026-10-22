package portal

import "time"

const MicroUSDT int64 = 1_000_000

type User struct {
	ID            string    `json:"id"`
	Email         string    `json:"email"`
	Role          string    `json:"role"`
	Enabled       bool      `json:"enabled"`
	EmailVerified bool      `json:"email_verified"`
	BalanceMicro  int64     `json:"balance_micro"`
	CreatedAt     time.Time `json:"created_at"`
}

func (u User) Admin() bool { return u.Role == "admin" || u.Role == "superadmin" }

type MailConfig struct {
	Protocol           string `json:"protocol"`
	Host               string `json:"host"`
	Port               int    `json:"port"`
	Username           string `json:"username"`
	Password           string `json:"password,omitempty"`
	PasswordConfigured bool   `json:"password_configured"`
	From               string `json:"from"`
}
type Pricing struct {
	Enabled             bool   `json:"enabled"`
	PerRunMicro         int64  `json:"per_run_micro"`
	PerKMMicro          int64  `json:"per_km_micro"`
	PlanPackageMicro    int64  `json:"plan_package_micro"`
	RechargeCodeShopURL string `json:"recharge_code_shop_url"`
	RechargeCodeEnabled bool   `json:"recharge_code_enabled"`
}
type WalletConfig struct {
	Enabled              bool   `json:"enabled"`
	Network              string `json:"network"`
	NodeURL              string `json:"node_url"`
	Contract             string `json:"contract"`
	NodeAPIKey           string `json:"node_api_key,omitempty"`
	NodeAPIKeyConfigured bool   `json:"node_api_key_configured"`
	SeedHex              string `json:"seed_hex,omitempty"`
	SeedConfigured       bool   `json:"seed_configured"`
	FeeeAPIKey           string `json:"feee_api_key,omitempty"`
	FeeeAPIKeyConfigured bool   `json:"feee_api_key_configured"`
	CollectionAddress    string `json:"collection_address"`
	AutoCollect          bool   `json:"auto_collect"`
	CollectCron          string `json:"collect_cron"`
	Timezone             string `json:"timezone"`
	MinCollectMicro      int64  `json:"min_collect_micro"`
	FeeLimitSun          int64  `json:"fee_limit_sun"`
	EnergyPriceSun       int64  `json:"energy_price_sun"`
}
type Settings struct {
	Mode                string       `json:"mode"`
	FrontendEnabled     bool         `json:"frontend_enabled"`
	RegistrationEnabled bool         `json:"registration_enabled"`
	EmailVerification   bool         `json:"email_verification"`
	Mail                MailConfig   `json:"mail"`
	Pricing             Pricing      `json:"pricing"`
	Wallet              WalletConfig `json:"wallet"`
}

func Defaults() Settings {
	return Settings{Mode: "personal", Mail: MailConfig{Protocol: "starttls", Port: 587}, Pricing: Pricing{RechargeCodeEnabled: true}, Wallet: WalletConfig{Network: "shasta", NodeURL: "https://api.shasta.trongrid.io", CollectCron: "0 3 * * *", Timezone: "Asia/Shanghai", MinCollectMicro: 5 * MicroUSDT, FeeLimitSun: 30_000_000, EnergyPriceSun: 100}}
}

type LedgerEntry struct {
	ID         string    `json:"id"`
	UserID     string    `json:"user_id"`
	DeltaMicro int64     `json:"delta_micro"`
	Kind       string    `json:"kind"`
	Reference  string    `json:"reference"`
	CreatedAt  time.Time `json:"created_at"`
}
type DepositAddress struct {
	UserID    string    `json:"user_id"`
	Address   string    `json:"address"`
	Index     uint32    `json:"index"`
	CreatedAt time.Time `json:"created_at"`
}
type Sweep struct {
	ID          string    `json:"id"`
	Address     string    `json:"address"`
	AmountMicro int64     `json:"amount_micro"`
	State       string    `json:"state"`
	TxID        string    `json:"tx_id"`
	Message     string    `json:"message"`
	CreatedAt   time.Time `json:"created_at"`
}
