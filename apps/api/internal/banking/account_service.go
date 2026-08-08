// Package banking implementa los casos de uso financieros.
//
// Coordina Postgres (metadatos, descripciones) y TigerBeetle (dinero), y hace
// cumplir las reglas que ninguna de las dos bases conoce por sí sola: sobre
// todo, que cada persona sólo puede tocar sus propias cuentas.
package banking

import (
	"context"
	"log/slog"
	"math/big"

	"github.com/Moisinho/banca-ai/apps/api/internal/domain"
	"github.com/Moisinho/banca-ai/apps/api/internal/ports"
)

// AccountService son los casos de uso sobre cuentas.
type AccountService struct {
	accounts ports.AccountRepository
	ledger   ports.Ledger
	log      *slog.Logger
}

func NewAccountService(accounts ports.AccountRepository, ledger ports.Ledger, log *slog.Logger) *AccountService {
	return &AccountService{accounts: accounts, ledger: ledger, log: log}
}

// AccountWithBalance combina los metadatos de una cuenta con su saldo.
//
// Los metadatos vienen de Postgres y el saldo de TigerBeetle: para el resto de
// la aplicación es una sola cosa, y esa unión ocurre acá.
type AccountWithBalance struct {
	Account domain.Account
	Balance domain.Balance
}

// ListByUser devuelve las cuentas de un usuario con sus saldos.
//
// Los saldos se piden en una sola llamada a TigerBeetle, no una por cuenta:
// consultar de a una sería el problema N+1.
func (s *AccountService) ListByUser(ctx context.Context, userID string) ([]AccountWithBalance, error) {
	accounts, err := s.accounts.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(accounts) == 0 {
		return []AccountWithBalance{}, nil
	}

	ids := make([]*big.Int, 0, len(accounts))
	for _, account := range accounts {
		ids = append(ids, account.TigerBeetleID)
	}

	balances, err := s.ledger.GetBalances(ctx, ids)
	if err != nil {
		return nil, err
	}

	out := make([]AccountWithBalance, 0, len(accounts))
	for _, account := range accounts {
		balance := balances[account.TigerBeetleID.String()]
		balance.AccountID = account.ID
		balance.AccountNumber = account.AccountNumber
		balance.Currency = account.Currency

		out = append(out, AccountWithBalance{Account: account, Balance: balance})
	}

	return out, nil
}

// GetByID devuelve una cuenta con su saldo, verificando que sea del usuario.
func (s *AccountService) GetByID(ctx context.Context, userID, accountID string) (AccountWithBalance, error) {
	account, err := s.accounts.FindByID(ctx, accountID)
	if err != nil {
		return AccountWithBalance{}, err
	}

	// Sin esta comprobación, cualquiera con un id de cuenta ajeno podría ver
	// el saldo de otra persona. Es la vulnerabilidad más común en APIs de este
	// tipo: exponer un recurso por id sin verificar quién lo pide.
	if account.UserID != userID {
		return AccountWithBalance{}, domain.ErrForbidden
	}

	balance, err := s.ledger.GetBalance(ctx, account.TigerBeetleID)
	if err != nil {
		return AccountWithBalance{}, err
	}

	balance.AccountID = account.ID
	balance.AccountNumber = account.AccountNumber
	balance.Currency = account.Currency

	return AccountWithBalance{Account: account, Balance: balance}, nil
}

// GetBalance devuelve sólo el saldo de una cuenta del usuario.
func (s *AccountService) GetBalance(ctx context.Context, userID, accountID string) (domain.Balance, error) {
	result, err := s.GetByID(ctx, userID, accountID)
	if err != nil {
		return domain.Balance{}, err
	}
	return result.Balance, nil
}

// PrimaryAccount devuelve la primera cuenta del usuario.
//
// La usa el chat con IA cuando la persona dice "mi cuenta" sin especificar
// cuál. Con una sola cuenta por usuario es inequívoco; si en el futuro hay
// varias, habrá que pedir aclaración.
func (s *AccountService) PrimaryAccount(ctx context.Context, userID string) (domain.Account, error) {
	accounts, err := s.accounts.ListByUser(ctx, userID)
	if err != nil {
		return domain.Account{}, err
	}
	if len(accounts) == 0 {
		return domain.Account{}, domain.ErrAccountNotFound
	}

	// ListByUser ordena por fecha de creación, así que la primera es la
	// cuenta original del registro.
	return accounts[0], nil
}

// FindOwnedByNumber busca una cuenta por su número y verifica que sea del
// usuario.
//
// La usa el chat con IA cuando la persona nombra una cuenta concreta. Sin esta
// comprobación, alguien podría pedirle a la IA el saldo de una cuenta ajena
// dando su número.
func (s *AccountService) FindOwnedByNumber(ctx context.Context, userID, accountNumber string) (domain.Account, error) {
	account, err := s.accounts.FindByNumber(ctx, accountNumber)
	if err != nil {
		return domain.Account{}, err
	}

	if account.UserID != userID {
		return domain.Account{}, domain.ErrForbidden
	}

	return account, nil
}
