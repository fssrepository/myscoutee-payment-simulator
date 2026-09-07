package simulator

import "regexp"

var guidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type BarionPayment struct {
	PaymentID             string                     `json:"PaymentId"`
	PaymentRequestID      string                     `json:"PaymentRequestId"`
	Status                string                     `json:"Status"`
	PaymentType           string                     `json:"PaymentType"`
	AllowedFundingSources []string                   `json:"AllowedFundingSources"`
	FundingSource         string                     `json:"FundingSource,omitempty"`
	PaymentMethod         string                     `json:"PaymentMethod"`
	GuestCheckout         bool                       `json:"GuestCheckout"`
	CreatedAt             string                     `json:"CreatedAt"`
	CompletedAt           string                     `json:"CompletedAt,omitempty"`
	ValidUntil            string                     `json:"ValidUntil"`
	DelayedCaptureUntil   string                     `json:"DelayedCaptureUntil,omitempty"`
	Transactions          []BarionPaymentTransaction `json:"Transactions"`
	Refunds               []BarionRefundTransaction  `json:"-"`
	Total                 float64                    `json:"Total"`
	Currency              string                     `json:"Currency"`
	SuggestedLocale       string                     `json:"SuggestedLocale"`
	CallbackURL           string                     `json:"CallbackUrl"`
	RedirectURL           string                     `json:"RedirectUrl"`
	GatewayURL            string                     `json:"GatewayUrl,omitempty"`
	LastOperation         string                     `json:"-"`
	ControlToken          string                     `json:"-"`
	RequestHash           string                     `json:"-"`
	CallbackDelivery      DeliveryAudit              `json:"-"`
}

type BarionPaymentTransaction struct {
	TransactionID    string       `json:"TransactionId"`
	POSTransactionID string       `json:"POSTransactionId"`
	Payee            string       `json:"Payee,omitempty"`
	Total            float64      `json:"Total"`
	OriginalTotal    float64      `json:"OriginalTotal,omitempty"`
	Currency         string       `json:"Currency"`
	Comment          string       `json:"Comment,omitempty"`
	Status           string       `json:"Status"`
	TransactionType  string       `json:"TransactionType"`
	RelatedID        string       `json:"RelatedId,omitempty"`
	Items            []BarionItem `json:"Items,omitempty"`
}

type BarionItem struct {
	Name        string  `json:"Name"`
	Description string  `json:"Description,omitempty"`
	Quantity    float64 `json:"Quantity"`
	Unit        string  `json:"Unit,omitempty"`
	UnitPrice   float64 `json:"UnitPrice"`
	ItemTotal   float64 `json:"ItemTotal"`
}

type barionRequestRecord struct {
	RequestHash string
	PaymentID   string
}

type BarionPaymentAudit struct {
	PaymentID           string                     `json:"payment_id"`
	PaymentRequestID    string                     `json:"payment_request_id"`
	Status              string                     `json:"status"`
	Total               float64                    `json:"total"`
	Currency            string                     `json:"currency"`
	DelayedCaptureUntil string                     `json:"delayed_capture_until,omitempty"`
	Transactions        []BarionPaymentTransaction `json:"transactions"`
	Refunds             []BarionRefundTransaction  `json:"refunds,omitempty"`
	LastOperation       string                     `json:"last_operation,omitempty"`
	CallbackDelivery    DeliveryAudit              `json:"callback_delivery"`
}

type BarionRefundTransaction struct {
	TransactionID    string  `json:"TransactionId"`
	POSTransactionID string  `json:"POSTransactionId"`
	Amount           float64 `json:"Total"`
	Comment          string  `json:"Comment,omitempty"`
	Status           string  `json:"Status"`
}

type barionStartRequest struct {
	POSKey               string                   `json:"POSKey"`
	PaymentType          string                   `json:"PaymentType"`
	GuestCheckout        bool                     `json:"GuestCheckOut"`
	FundingSources       []string                 `json:"FundingSources"`
	PaymentRequestID     string                   `json:"PaymentRequestId"`
	RedirectURL          string                   `json:"RedirectUrl"`
	CallbackURL          string                   `json:"CallbackUrl"`
	Transactions         []barionStartTransaction `json:"Transactions"`
	Locale               string                   `json:"Locale"`
	Currency             string                   `json:"Currency"`
	DelayedCapturePeriod string                   `json:"DelayedCapturePeriod"`
	PaymentWindow        string                   `json:"PaymentWindow"`
	RecurrenceType       string                   `json:"RecurrenceType"`
	TraceID              string                   `json:"TraceId"`
}

type barionStartTransaction struct {
	POSTransactionID string       `json:"POSTransactionId"`
	Payee            string       `json:"Payee"`
	Total            float64      `json:"Total"`
	Comment          string       `json:"Comment"`
	Items            []BarionItem `json:"Items"`
}

type barionStartResponse struct {
	PaymentID        string                     `json:"PaymentId"`
	PaymentRequestID string                     `json:"PaymentRequestId"`
	Status           string                     `json:"Status"`
	GatewayURL       string                     `json:"GatewayUrl"`
	CallbackURL      string                     `json:"CallbackUrl"`
	RedirectURL      string                     `json:"RedirectUrl"`
	Transactions     []BarionPaymentTransaction `json:"Transactions"`
	Errors           []barionAPIError           `json:"Errors"`
}

type barionFinishRequest struct {
	POSKey       string                      `json:"POSKey"`
	PaymentID    string                      `json:"PaymentId"`
	Transactions []barionTransactionToFinish `json:"Transactions"`
}

type barionTransactionToFinish struct {
	TransactionID string  `json:"TransactionId"`
	Total         float64 `json:"Total"`
	Comment       string  `json:"Comment"`
}

type barionFinishResponse struct {
	IsSuccessful     bool                       `json:"IsSuccessful"`
	PaymentID        string                     `json:"PaymentId"`
	PaymentRequestID string                     `json:"PaymentRequestId"`
	Status           string                     `json:"Status"`
	Transactions     []BarionPaymentTransaction `json:"Transactions"`
	Errors           []barionAPIError           `json:"Errors"`
}

type barionRefundRequest struct {
	POSKey               string                      `json:"POSKey"`
	PaymentID            string                      `json:"PaymentId"`
	TransactionsToRefund []barionTransactionToRefund `json:"TransactionsToRefund"`
}

type barionTransactionToRefund struct {
	TransactionID    string  `json:"TransactionId"`
	POSTransactionID string  `json:"POSTransactionId"`
	AmountToRefund   float64 `json:"AmountToRefund"`
	Comment          string  `json:"Comment"`
}

type barionRefundResponse struct {
	PaymentID             string                    `json:"PaymentId"`
	RefundedTransactions  []BarionRefundTransaction `json:"RefundedTransactions"`
	Errors                []barionAPIError          `json:"Errors"`
}

type barionAPIError struct {
	ErrorCode   string `json:"ErrorCode"`
	Title       string `json:"Title"`
	Description string `json:"Description"`
}
