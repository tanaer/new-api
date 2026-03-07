package model

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
)

// Supplier 供应商主表
type Supplier struct {
	Id                 int     `json:"id" gorm:"primaryKey;autoIncrement"`
	Name               string  `json:"name" gorm:"type:varchar(255);not null"`
	BaseURL            string  `json:"base_url" gorm:"type:varchar(1024);not null"`
	Username           string  `json:"username" gorm:"type:varchar(255);default:''"`
	Password           string  `json:"password" gorm:"type:varchar(255);default:''"`
	Cookie             string  `json:"cookie" gorm:"type:text"`
	UpstreamUserID     int     `json:"upstream_user_id" gorm:"default:0"`
	Markup             float64 `json:"markup" gorm:"default:1.1"`
	Status             int     `json:"status" gorm:"default:1"` // 1=启用 2=禁用
	Balance            float64 `json:"balance"`
	BalanceUpdatedTime int64   `json:"balance_updated_time" gorm:"bigint"`
	CreatedTime        int64   `json:"created_time" gorm:"bigint"`
	UpdatedTime        int64   `json:"updated_time" gorm:"bigint"`
}

// SupplierGroup 供应商分组映射表
type SupplierGroup struct {
	Id            int     `json:"id" gorm:"primaryKey;autoIncrement"`
	SupplierID    int     `json:"supplier_id" gorm:"index;not null"`
	UpstreamGroup string  `json:"upstream_group" gorm:"type:varchar(64);not null"`
	ApiKey        string  `json:"api_key" gorm:"type:text"`
	LocalGroup    string  `json:"local_group" gorm:"type:varchar(64);default:''"`
	GroupRatio    float64 `json:"group_ratio" gorm:"default:1.0"`
	// 新增字段
	SupportedModels string `json:"supported_models" gorm:"type:text"`                      // 该分组支持的模型列表，逗号分隔
	EndpointType    string `json:"endpoint_type" gorm:"type:varchar(32);default:'openai'"` // 通道类型
	EndpointTypes   string `json:"endpoint_types" gorm:"type:text"`                        // 支持的端点类型列表，JSON 数组
	Status          int    `json:"status" gorm:"default:1"`
}

// SupplierGroupSyncLog 分组倍率同步日志
type SupplierGroupSyncLog struct {
	Id           int    `json:"id" gorm:"primaryKey;autoIncrement"`
	SupplierID   int    `json:"supplier_id" gorm:"index"`
	SupplierName string `json:"supplier_name" gorm:"type:varchar(255)"`
	SyncType     string `json:"sync_type" gorm:"type:varchar(32)"` // ratio_change, group_added, group_removed
	Details      string `json:"details" gorm:"type:text"`
	CreatedTime  int64  `json:"created_time" gorm:"bigint"`
}

// ========== Supplier CRUD ==========

func CreateSupplier(supplier *Supplier) error {
	supplier.CreatedTime = common.GetTimestamp()
	supplier.UpdatedTime = common.GetTimestamp()
	return DB.Create(supplier).Error
}

func GetAllSuppliers() ([]*Supplier, error) {
	var suppliers []*Supplier
	err := DB.Order("id desc").Find(&suppliers).Error
	return suppliers, err
}

func GetSupplierById(id int) (*Supplier, error) {
	var supplier Supplier
	err := DB.First(&supplier, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &supplier, nil
}

func UpdateSupplier(supplier *Supplier) error {
	supplier.UpdatedTime = common.GetTimestamp()
	return DB.Model(supplier).Updates(supplier).Error
}

func DeleteSupplier(id int) error {
	// 级联删除分组
	if err := DB.Where("supplier_id = ?", id).Delete(&SupplierGroup{}).Error; err != nil {
		return err
	}
	// 删除该供应商的所有渠道
	if err := DB.Where("supplier_id = ?", id).Delete(&Channel{}).Error; err != nil {
		common.SysLog(fmt.Sprintf("failed to delete channels for supplier: supplier_id=%d, error=%v", id, err))
	}
	return DB.Delete(&Supplier{}, "id = ?", id).Error
}

func GetAllEnabledSuppliers() ([]*Supplier, error) {
	var suppliers []*Supplier
	err := DB.Where("status = ?", common.ChannelStatusEnabled).Find(&suppliers).Error
	return suppliers, err
}

// ========== SupplierGroup CRUD ==========

func GetSupplierGroups(supplierID int) ([]*SupplierGroup, error) {
	var groups []*SupplierGroup
	err := DB.Where("supplier_id = ?", supplierID).Order("id asc").Find(&groups).Error
	return groups, err
}

func CreateSupplierGroup(group *SupplierGroup) error {
	return DB.Create(group).Error
}

func UpdateSupplierGroup(group *SupplierGroup) error {
	return DB.Model(group).Updates(group).Error
}

func DeleteSupplierGroup(id int) error {
	return DB.Delete(&SupplierGroup{}, "id = ?", id).Error
}

func GetSupplierGroupByUpstream(supplierID int, upstreamGroup string) (*SupplierGroup, error) {
	var group SupplierGroup
	err := DB.Where("supplier_id = ? AND upstream_group = ?", supplierID, upstreamGroup).First(&group).Error
	if err != nil {
		return nil, err
	}
	return &group, nil
}

func GetMaxGroupRatioBySupplier(supplierID int) (float64, error) {
	var maxRatio float64
	err := DB.Model(&SupplierGroup{}).Where("supplier_id = ?", supplierID).
		Select("COALESCE(MAX(group_ratio), 1.0)").Scan(&maxRatio).Error
	return maxRatio, err
}

// GetChannelsBySupplierID 获取供应商下所有通道
func GetChannelsBySupplierID(supplierID int) ([]*Channel, error) {
	var channels []*Channel
	err := DB.Where("supplier_id = ?", supplierID).Find(&channels).Error
	return channels, err
}

// BatchUpdateSupplierMarkup 批量更新所有供应商倍率
func BatchUpdateSupplierMarkup(markup float64) error {
	return DB.Model(&Supplier{}).Where("1 = 1").Update("markup", markup).Error
}

// ========== SupplierGroupSyncLog CRUD ==========

func CreateSyncLog(log *SupplierGroupSyncLog) error {
	log.CreatedTime = common.GetTimestamp()
	return DB.Create(log).Error
}

func GetSyncLogs(startIdx int, num int, supplierID int) ([]*SupplierGroupSyncLog, int64, error) {
	var logs []*SupplierGroupSyncLog
	var total int64

	query := DB.Model(&SupplierGroupSyncLog{})
	if supplierID > 0 {
		query = query.Where("supplier_id = ?", supplierID)
	}
	query.Count(&total)

	err := query.Order("id desc").Offset(startIdx).Limit(num).Find(&logs).Error
	return logs, total, err
}
