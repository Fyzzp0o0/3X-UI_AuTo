package chain

import (
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// AuditRecord is one security-relevant action. Detail must already be redacted:
// no passwords, tokens or full links may be stored here.
type AuditRecord struct {
	Action     string
	ObjectType string
	ObjectID   int
	Detail     string
}

// Audit appends one audit row. Callers log-and-continue: a failed audit must
// not fail the operation that already succeeded.
func (s *Service) Audit(actor, ip, ua string, r AuditRecord) error {
	row := &model.ChainAudit{
		At: time.Now().UnixMilli(), Actor: truncate(actor, 64), Action: truncate(r.Action, 32),
		ObjectType: truncate(r.ObjectType, 24), ObjectId: r.ObjectID,
		Ip: truncate(ip, 64), Ua: truncate(ua, 255), Detail: truncate(r.Detail, 500),
	}
	return database.GetDB().Create(row).Error
}

// ListAudit returns the newest rows first.
func (s *Service) ListAudit(page, size int) ([]model.ChainAudit, int64, error) {
	db := database.GetDB().Model(&model.ChainAudit{})
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	size, page = normalizePage(size, page)
	var rows []model.ChainAudit
	err := db.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error
	return rows, total, err
}
