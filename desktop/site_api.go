package main

import (
	"asmroner/internal/engine"
	"asmroner/internal/model"
	"asmroner/internal/utils"
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

type DesktopSiteFacet struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type DesktopSiteLoginState struct {
	LoggedIn bool   `json:"loggedIn"`
	Name     string `json:"name"`
}

type DesktopSiteTrack struct {
	Type      string             `json:"type"`
	Title     string             `json:"title"`
	StreamURL string             `json:"streamUrl,omitempty"`
	Children  []DesktopSiteTrack `json:"children,omitempty"`
}

type DesktopSiteWorkDetail struct {
	SourceID      string             `json:"sourceId"`
	Title         string             `json:"title"`
	Circle        string             `json:"circle"`
	Release       string             `json:"release"`
	Rating        float64            `json:"rating"`
	DownloadCount int                `json:"downloadCount"`
	HasSubtitle   bool               `json:"hasSubtitle"`
	Duration      int                `json:"duration"`
	CoverURL      string             `json:"coverUrl"`
	SourceURL     string             `json:"sourceUrl"`
	Tags          []string           `json:"tags"`
	VoiceActors   []string           `json:"voiceActors"`
	Tracks        []DesktopSiteTrack `json:"tracks"`
	PlayableCount int                `json:"playableCount"`
}

// BrowseSiteWorks powers the native asmr.one views. No website is embedded:
// the desktop window requests the same public JSON endpoints and renders them
// with the application's own components.
func (a *App) BrowseSiteWorks(section string, keyword string, page int, pageSize int) (DesktopSearchPage, error) {
	section = strings.ToLower(strings.TrimSpace(section))
	keyword = strings.TrimSpace(keyword)
	if page < 1 {
		page = 1
	}
	if pageSize < 8 || pageSize > 50 {
		pageSize = 20
	}

	if section == "works" && keyword != "" {
		return a.SearchPage(keyword, page, pageSize)
	}
	if section != "works" && section != "popular" && section != "random" {
		return DesktopSearchPage{}, errors.New("不支持的站点栏目")
	}
	if section == "random" {
		page = 1
		pageSize = 1
	}

	manager, err := engine.NewEngineManager()
	if err != nil {
		return DesktopSearchPage{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result := model.SearchResult{}
	request := manager.Client.R().
		SetContext(ctx).
		SetHeader("Accept", "application/json, text/plain, */*").
		SetHeader("Origin", "https://asmr.one").
		SetHeader("Referer", "https://asmr.one/").
		SetHeader("Authorization", manager.JWTToken).
		SetResult(&result)

	var responseStatus string
	if section == "popular" {
		response, requestErr := request.
			SetHeader("Content-Type", "application/json").
			SetBody(map[string]interface{}{
				"keyword":             keyword,
				"page":                page,
				"pageSize":            pageSize,
				"subtitle":            0,
				"localSubtitledWorks": []interface{}{},
				"withPlaylistStatus":  []interface{}{},
			}).
			Post(manager.ApiUrl + "/api/recommender/popular")
		if requestErr != nil {
			return DesktopSearchPage{}, requestErr
		}
		responseStatus = response.Status()
		if !response.IsSuccess() {
			return DesktopSearchPage{}, fmt.Errorf("热门作品请求失败: %s", responseStatus)
		}
	} else {
		query := map[string]string{
			"order":    "release",
			"sort":     "desc",
			"page":     strconv.Itoa(page),
			"pageSize": strconv.Itoa(pageSize),
		}
		if section == "random" {
			query["order"] = "betterRandom"
			query["sort"] = ""
		}
		response, requestErr := request.
			SetQueryParams(query).
			Get(manager.ApiUrl + "/api/works")
		if requestErr != nil {
			return DesktopSearchPage{}, requestErr
		}
		responseStatus = response.Status()
		if !response.IsSuccess() {
			return DesktopSearchPage{}, fmt.Errorf("作品列表请求失败: %s", responseStatus)
		}
	}

	return desktopSearchPageFromResult(result, page, pageSize), nil
}

// GetSiteCatalog returns the complete public circle, tag, or voice-actor index.
// The frontend caches it and handles instant filtering and pagination locally.
func (a *App) GetSiteCatalog(kind string) ([]DesktopSiteFacet, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	path, ok := map[string]string{
		"circles": "/api/circles/",
		"tags":    "/api/tags/",
		"vas":     "/api/vas/",
	}[kind]
	if !ok {
		return nil, errors.New("不支持的站点目录")
	}

	manager, err := engine.NewEngineManager()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	var result []struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	response, err := manager.Client.R().
		SetContext(ctx).
		SetHeader("Accept", "application/json, text/plain, */*").
		SetHeader("Origin", "https://asmr.one").
		SetHeader("Referer", "https://asmr.one/").
		SetHeader("Authorization", manager.JWTToken).
		SetResult(&result).
		Get(manager.ApiUrl + path)
	if err != nil {
		return nil, err
	}
	if !response.IsSuccess() {
		return nil, fmt.Errorf("站点目录请求失败: %s", response.Status())
	}

	items := make([]DesktopSiteFacet, 0, len(result))
	for _, item := range result {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		items = append(items, DesktopSiteFacet{Name: name, Count: item.Count})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Count == items[j].Count {
			return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
		}
		return items[i].Count > items[j].Count
	})
	return items, nil
}

func (a *App) SiteLogin(account string, password string) (DesktopSiteLoginState, error) {
	account = strings.TrimSpace(account)
	if account == "" || password == "" {
		return DesktopSiteLoginState{}, errors.New("请输入账号和密码")
	}

	manager, err := engine.NewEngineManager()
	if err != nil {
		return DesktopSiteLoginState{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	var result struct {
		User struct {
			LoggedIn bool   `json:"loggedIn"`
			Name     string `json:"name"`
		} `json:"user"`
		Token string `json:"token"`
	}
	response, err := manager.Client.R().
		SetContext(ctx).
		SetHeader("Accept", "application/json, text/plain, */*").
		SetHeader("Content-Type", "application/json").
		SetHeader("Origin", "https://asmr.one").
		SetHeader("Referer", "https://asmr.one/").
		SetResult(&result).
		SetBody(map[string]string{"name": account, "password": password}).
		Post(manager.ApiUrl + "/api/auth/me")
	if err != nil {
		return DesktopSiteLoginState{}, err
	}
	if !response.IsSuccess() || !result.User.LoggedIn || result.Token == "" {
		return DesktopSiteLoginState{}, errors.New("登录失败，请检查账号、密码或网络状态")
	}

	settings := a.GetState().Settings
	settings.Account = account
	settings.Password = password
	if err := a.SaveSettings(settings); err != nil {
		return DesktopSiteLoginState{}, fmt.Errorf("登录成功，但保存账号失败: %w", err)
	}
	return DesktopSiteLoginState{LoggedIn: true, Name: result.User.Name}, nil
}

func (a *App) GetSiteWorkDetail(sourceID string) (DesktopSiteWorkDetail, error) {
	sourceID = strings.ToUpper(strings.TrimSpace(sourceID))
	valid, _, number, err := utils.IsValidDlsiteID(sourceID)
	if err != nil || !valid {
		return DesktopSiteWorkDetail{}, errors.New("作品编号格式不正确")
	}

	manager, err := engine.NewEngineManager()
	if err != nil {
		return DesktopSiteWorkDetail{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	work, err := manager.GetWorkInfo(ctx, number)
	if err != nil {
		return DesktopSiteWorkDetail{}, err
	}
	tracks, err := manager.GetVoiceTracks(number)
	if err != nil {
		return DesktopSiteWorkDetail{}, err
	}

	tags := make([]string, 0, len(work.Tags))
	for _, tag := range work.Tags {
		if name := strings.TrimSpace(tag.Name); name != "" {
			tags = append(tags, name)
		}
	}
	voiceActors := make([]string, 0, len(work.Vas))
	for _, actor := range work.Vas {
		if name := strings.TrimSpace(actor.Name); name != "" {
			voiceActors = append(voiceActors, name)
		}
	}
	desktopTracks, playableCount := convertSiteTracks(tracks)
	return DesktopSiteWorkDetail{
		SourceID:      work.SourceID,
		Title:         work.Title,
		Circle:        work.Circle.Name,
		Release:       work.Release,
		Rating:        float64(work.RateAverage2Dp),
		DownloadCount: work.DlCount,
		HasSubtitle:   work.HasSubtitle,
		Duration:      work.Duration,
		CoverURL:      work.MainCoverURL,
		SourceURL:     work.SourceURL,
		Tags:          tags,
		VoiceActors:   voiceActors,
		Tracks:        desktopTracks,
		PlayableCount: playableCount,
	}, nil
}

func convertSiteTracks(tracks []model.Track) ([]DesktopSiteTrack, int) {
	result := make([]DesktopSiteTrack, 0, len(tracks))
	playableCount := 0
	for _, track := range tracks {
		children, childPlayableCount := convertSiteTracks(track.Children)
		item := DesktopSiteTrack{
			Type:      track.Type,
			Title:     track.Title,
			StreamURL: track.MediaStreamURL,
			Children:  children,
		}
		if strings.EqualFold(track.Type, "audio") && strings.TrimSpace(track.MediaStreamURL) != "" {
			playableCount++
		}
		playableCount += childPlayableCount
		result = append(result, item)
	}
	return result, playableCount
}

func desktopSearchPageFromResult(result model.SearchResult, fallbackPage int, fallbackPageSize int) DesktopSearchPage {
	works := make([]DesktopSearchWork, 0, len(result.Works))
	for _, work := range result.Works {
		tags := make([]string, 0, min(4, len(work.Tags)))
		for _, tag := range work.Tags {
			if name := strings.TrimSpace(tag.Name); name != "" {
				tags = append(tags, name)
				if len(tags) == 4 {
					break
				}
			}
		}
		works = append(works, DesktopSearchWork{
			SourceID:      work.SourceID,
			Title:         work.Title,
			Release:       work.Release,
			Rating:        work.RateAverage2Dp,
			DownloadCount: work.DlCount,
			HasSubtitle:   work.HasSubtitle,
			CoverURL:      work.ThumbnailCoverURL,
			Circle:        work.Circle.Name,
			Duration:      work.Duration,
			Tags:          tags,
		})
	}
	page := result.Pagination.CurrentPage
	if page < 1 {
		page = fallbackPage
	}
	pageSize := result.Pagination.PageSize
	if pageSize < 1 {
		pageSize = fallbackPageSize
	}
	totalPages := 0
	if result.Pagination.TotalCount > 0 && pageSize > 0 {
		totalPages = (result.Pagination.TotalCount + pageSize - 1) / pageSize
	}
	return DesktopSearchPage{
		Works:      works,
		Page:       page,
		PageSize:   pageSize,
		Total:      result.Pagination.TotalCount,
		TotalPages: totalPages,
	}
}
