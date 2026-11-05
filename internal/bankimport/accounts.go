package bankimport

import (
	"crypto/sha256"
	"errors"
	"strings"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

// ErrAccountMapping prevents a single-account caller from merging source accounts.
var ErrAccountMapping = errors.New("bank import: multiple source accounts require mapping")

// SourceAccount groups a statement without retaining its full account number.
type SourceAccount struct {
	Name     string
	Suffix   string
	Payments []store.BankTransaction
}

func singleAccount(accounts []SourceAccount, err error) ([]store.BankTransaction, error) {
	if err != nil {
		return nil, err
	}
	if len(accounts) != 1 {
		return nil, ErrAccountMapping
	}
	return accounts[0].Payments, nil
}

func accountForRow(row tableRow, c columns) ([32]byte, SourceAccount, error) {
	get := func(index int) string {
		if index < 0 || index >= len(row.cells) {
			return ""
		}
		return strings.TrimSpace(row.cells[index].text)
	}
	name, number := get(c.accountName), get(c.account)
	if c.account < 0 && c.accountName < 0 {
		return [32]byte{}, SourceAccount{}, nil
	}
	if (c.account >= 0 && number == "") || (c.account < 0 && name == "") {
		return [32]byte{}, SourceAccount{}, tableError("account", row.number)
	}
	account := SourceAccount{Name: strings.Clone(name)}
	key := "name:" + strings.ToLower(strings.Join(strings.Fields(name), " "))
	if c.account >= 0 {
		normalized := strings.ToUpper(strings.Join(strings.Fields(number), ""))
		key = "number:" + normalized
		runes := []rune(normalized)
		account.Suffix = string(runes[max(0, len(runes)-4):])
	}
	return sha256.Sum256([]byte(key)), account, nil
}
