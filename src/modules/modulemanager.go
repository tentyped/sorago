package modules

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

const modulesFileName = "modules.json"

type ModuleManager struct {
  modules   []ScrapingModule
  mutex     sync.Mutex
  filePath  string
  logger    *zap.Logger
}

func NewModuleManager(storageDir string, logger *zap.Logger) *ModuleManager {

  manager := &ModuleManager{
    filePath: filepath.Join(storageDir, modulesFileName),
    logger: logger,
  }
  manager.LoadModules()
  return manager
}

func (m *ModuleManager) LoadModules() {
  m.mutex.Lock()
  defer m.mutex.Unlock()

  data, err := os.ReadFile(m.filePath)
  if err != nil {
    if !os.IsNotExist(err) {
      m.logger.Warn("Failed to load modules", zap.Error(err))
    }
    return
  }

  if err := json.Unmarshal(data, &m.modules); err != nil {
    m.logger.Warn("Failed to parse modules JSON", zap.Error(err))
  }
}

func (m* ModuleManager) SaveModules() {
  data, err := json.MarshalIndent(m.modules, "", " ")
  if err != nil {
    m.logger.Error("Failed to encode modules", zap.Error(err))
    return
  }

  if err := os.WriteFile(m.filePath, data, 0644); err != nil {
    m.logger.Error("Failed to save modules", zap.Error(err))
  }
}

func (m *ModuleManager) AddModule(metadataURL string, storageDir string) (*ScrapingModule, error) {
  m.mutex.Lock()
  defer m.mutex.Unlock()

  for _, mod := range m.modules {
    if mod.MetadataURL == metadataURL {
      return nil, errors.New("module already exists")
    }
  }

  resp, err := http.Get(metadataURL)
  if err != nil {
    return nil, err
  }
  defer resp.Body.Close()

  var metadata ModuleMetadata
  if err := json.NewDecoder(resp.Body).Decode(&metadata); err != nil {
    return nil, err
  }

  resp, err = http.Get(metadata.ScriptURL)
  if err != nil {
    return nil, err
  }
  defer resp.Body.Close()

  scriptData, err := io.ReadAll(resp.Body)
  if err != nil {
    return nil, err
  }

  fileName := uuid.New().String() + ".js"
  scriptPath := filepath.Join(storageDir, fileName)
	if err := os.WriteFile(scriptPath, scriptData, 0644); err != nil {
		return nil, err
	}

	// Create new module
	module := ScrapingModule{
		ID:          uuid.New(),
		Metadata:    metadata,
		LocalPath:   fileName,
		MetadataURL: metadataURL,
		IsActive:    false,
	}

	m.modules = append(m.modules, module)
	m.SaveModules()

	m.logger.Info("Added module", zap.String("source", metadata.SourceName))
	return &module, nil
}

func (m *ModuleManager) DeleteModule(moduleID uuid.UUID, storageDir string) error {
  m.mutex.Lock()
  defer m.mutex.Unlock()

  for i, mod := range m.modules {
    if mod.ID == moduleID {
      scriptPath := filepath.Join(storageDir, mod.LocalPath)
      if err := os.Remove(scriptPath); err != nil && !os.IsNotExist(err) {
        m.logger.Warn("Failed to delete module script", zap.Error(err))
      }

      m.modules = append(m.modules[:i], m.modules[i+1:]...)
      m.SaveModules()

      m.logger.Info("Deleted module", zap.String("source", mod.Metadata.SourceName))
      return nil
    }
  }
  return errors.New("module not found")
}

func (m *ModuleManager) GetModules() []map[string]string {
  m.mutex.Lock()
  defer m.mutex.Unlock()
  
  var modulesList []map[string]string
  for _, mod := range m.modules {
    modulesList = append(modulesList, map[string]string{
      "id": mod.ID.String(),
      "name": mod.Metadata.SourceName,
    })
  }

  return modulesList
}

func (m *ModuleManager) GetModuleContent(moduleID uuid.UUID, storageDir string) (string, error) {
  m.mutex.Lock()
  defer m.mutex.Unlock()

  for _, mod := range m.modules {
    if mod.ID == moduleID {
      scriptPath := filepath.Join(storageDir, mod.LocalPath)
      content, err := os.ReadFile(scriptPath)
      if err != nil {
        return "", err
      }
      return string(content), nil
    }
  }
  return "", errors.New("module not found")
}

func (m *ModuleManager) RefreshModules(storageDir string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	for i, mod := range m.modules {
		resp, err := http.Get(mod.MetadataURL)
		if err != nil {
			m.logger.Warn("Failed to refresh module", zap.String("source", mod.Metadata.SourceName), zap.Error(err))
			continue
		}
		defer resp.Body.Close()

		var newMetadata ModuleMetadata
		if err := json.NewDecoder(resp.Body).Decode(&newMetadata); err != nil {
			m.logger.Warn("Failed to decode updated metadata", zap.String("source", mod.Metadata.SourceName), zap.Error(err))
			continue
		}

		if newMetadata.Version == mod.Metadata.Version {
			continue
		}

		resp, err = http.Get(newMetadata.ScriptURL)
		if err != nil {
			m.logger.Warn("Failed to fetch updated script", zap.String("source", mod.Metadata.SourceName), zap.Error(err))
			continue
		}
		defer resp.Body.Close()

		scriptData, err := io.ReadAll(resp.Body)
		if err != nil {
			m.logger.Warn("Failed to read updated script", zap.String("source", mod.Metadata.SourceName), zap.Error(err))
			continue
		}

		scriptPath := filepath.Join(storageDir, mod.LocalPath)
		if err := os.WriteFile(scriptPath, scriptData, 0644); err != nil {
			m.logger.Warn("Failed to save updated script", zap.String("source", mod.Metadata.SourceName), zap.Error(err))
			continue
		}

		mod.Metadata = newMetadata
		m.modules[i] = mod
		m.logger.Info("Updated module", zap.String("source", mod.Metadata.SourceName), zap.String("version", newMetadata.Version))
	}

	m.SaveModules()
}
