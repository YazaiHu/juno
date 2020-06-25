package confgov2

import (
	"fmt"
	"github.com/douyu/juno/internal/pkg/service/codec/util"
	db2 "github.com/douyu/juno/pkg/model/db"
	view2 "github.com/douyu/juno/pkg/model/view"
	"github.com/jinzhu/gorm"
	"sync"
)

var (
	ErrConfigNotExists error = fmt.Errorf("配置不存在")
)

func List(param view2.ReqListConfig) (resp view2.RespListConfig, err error) {
	resp = make(view2.RespListConfig, 0)
	list := make([]db2.Configuration, 0)
	err = mysql.Select("id, aid, name, format, env, zone, created_at, updated_at, published_at").
		Where("aid = ?", param.AID).
		Where("env = ?", param.Env).
		Find(&list).Error

	for _, item := range list {
		resp = append(resp, view2.RespListConfigItem{
			ID:          item.ID,
			AID:         item.AID,
			Name:        item.Name,
			Format:      item.Format,
			Env:         item.Env,
			Zone:        item.Zone,
			CreatedAt:   item.CreatedAt,
			UpdatedAt:   item.UpdatedAt,
			DeletedAt:   item.DeletedAt,
			PublishedAt: item.PublishedAt,
		})
	}

	return
}

func Detail(param view2.ReqDetailConfig) (resp view2.RespDetailtConfig, err error) {
	configuration := db2.Configuration{}
	err = mysql.Where("id = ?", param.ID).First(&configuration).Error
	if err != nil {
		return
	}

	resp = view2.RespDetailtConfig{
		ID:          configuration.ID,
		AID:         configuration.AID,
		Name:        configuration.Name,
		Content:     configuration.Content,
		Format:      configuration.Format,
		Env:         configuration.Env,
		Zone:        configuration.Zone,
		CreatedAt:   configuration.CreatedAt,
		UpdatedAt:   configuration.UpdatedAt,
		PublishedAt: configuration.PublishedAt,
	}

	return
}

func Create(param view2.ReqCreateConfig) (err error) {

	tx := mysql.Begin()
	{
		// check if name exists
		exists := 0
		err = tx.Model(&db2.Configuration{}).Where("aid = ?", param.AID).
			Where("env = ?", param.Env).
			Where("name = ?", param.FileName).
			Where("format = ?", param.Format).
			Count(&exists).Error
		if err != nil {
			tx.Rollback()
			return err
		}

		if exists != 0 {
			tx.Rollback()
			return fmt.Errorf("已存在同名配置")
		}

		configuration := db2.Configuration{
			AID:    param.AID,
			Name:   param.FileName, // 不带后缀
			Format: string(param.Format),
			Env:    param.Env,
			Zone:   param.Zone,
		}

		err = tx.Create(&configuration).Error
		if err != nil {
			tx.Rollback()
			return
		}
	}

	err = tx.Commit().Error
	if err != nil {
		tx.Rollback()
		return err
	}

	return
}

func Update(uid int, param view2.ReqUpdateConfig) (err error) {
	configuration := db2.Configuration{}
	err = mysql.Where("id = ?", param.ID).First(&configuration).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return ErrConfigNotExists
		}

		return err
	}

	// 计算本次版本号
	version := util.Md5Str(param.Content)
	if util.Md5Str(configuration.Content) == version {
		return fmt.Errorf("保存失败，本次无更新")
	}

	history := db2.ConfigurationHistory{
		UID:             uint(uid),
		ConfigurationID: configuration.ID,
		ChangeLog:       param.Message,
		Content:         param.Content,
		Version:         version,
	}

	tx := mysql.Begin()
	{
		// 存历史版本
		err = mysql.Save(&history).Error
		if err != nil {
			tx.Rollback()
			return err
		}

		configuration.Content = param.Content
		err = mysql.Save(&configuration).Error
		if err != nil {
			tx.Rollback()
			return err
		}
	}

	err = tx.Commit().Error
	if err != nil {
		tx.Rollback()
		return err
	}

	return
}

func Publish(param view2.ReqPublishConfig) (err error) {
	//TODO: 完成配置发布逻辑
	return
}

//History 发布历史分页列表，Page从0开始
func History(param view2.ReqHistoryConfig) (resp view2.RespHistoryConfig, err error) {
	list := make([]db2.ConfigurationHistory, 0)

	if param.Size == 0 {
		param.Size = 1
	}

	query := mysql.Where("configuration_id = ?", param.ID)

	wg := sync.WaitGroup{}
	errChan := make(chan error)
	doneChan := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()

		offset := param.Size * param.Page
		query := query.Preload("User").Limit(param.Size).Offset(offset).Order("id desc").Find(&list)
		if query.Error != nil {
			errChan <- query.Error
		}
	}()

	wg.Add(1)
	go func() {
		wg.Done()

		q := query.Model(&db2.ConfigurationHistory{}).Count(&resp.Pagination.Total)
		if q.Error != nil {
			errChan <- q.Error
		}
	}()

	go func() {
		wg.Wait()

		doneChan <- struct{}{}
	}()

	select {
	case <-doneChan:
		break
	case e := <-errChan:
		close(errChan)
		err = e
		return
	}

	for _, item := range list {
		configItem := view2.RespHistoryConfigItem{
			ID:              item.ID,
			UID:             item.UID,
			ConfigurationID: item.ConfigurationID,
			Version:         item.Version,
			CreatedAt:       item.CreatedAt,
			ChangeLog:       item.ChangeLog,
		}

		if item.User != nil {
			configItem.UserName = item.User.Username
		}

		resp.List = append(resp.List, configItem)
	}

	resp.Pagination.Current = int(param.Page)
	resp.Pagination.PageSize = int(param.Size)

	return
}

func Diff(id uint) (resp view2.RespDiffConfig, err error) {
	modifiedConfig := db2.ConfigurationHistory{}
	err = mysql.Preload("Configuration").Preload("User").
		Where("id = ?", id).First(&modifiedConfig).Error
	if err != nil {
		return
	}

	originConfig := db2.ConfigurationHistory{}
	err = mysql.Preload("Configuration").Preload("User").
		Where("id < ?", id).Order("id desc").First(&originConfig).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			resp.Origin = nil
			err = nil
		} else {
			return
		}
	} else {
		resp.Origin = &view2.RespDetailtConfig{
			ID:          originConfig.ID,
			AID:         originConfig.Configuration.AID,
			Name:        originConfig.Configuration.Name,
			Content:     originConfig.Content,
			Format:      originConfig.Configuration.Format,
			Env:         originConfig.Configuration.Env,
			Zone:        originConfig.Configuration.Env,
			CreatedAt:   originConfig.CreatedAt,
			UpdatedAt:   originConfig.Configuration.UpdatedAt,
			PublishedAt: originConfig.Configuration.PublishedAt,
		}
	}

	resp.Modified = view2.RespDetailtConfig{
		ID:          modifiedConfig.ID,
		AID:         modifiedConfig.Configuration.AID,
		Name:        modifiedConfig.Configuration.Name,
		Content:     modifiedConfig.Content,
		Format:      modifiedConfig.Configuration.Format,
		Env:         modifiedConfig.Configuration.Env,
		Zone:        modifiedConfig.Configuration.Env,
		CreatedAt:   modifiedConfig.CreatedAt,
		UpdatedAt:   modifiedConfig.Configuration.UpdatedAt,
		PublishedAt: modifiedConfig.Configuration.PublishedAt,
	}

	return
}

func Delete(id uint) (err error) {
	err = mysql.Delete(&db2.Configuration{}, "id = ?", id).Error
	return
}