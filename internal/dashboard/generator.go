package dashboard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/adilsoncruz/obsynx/internal/config"
)

// DefaultOutputDir is the standard Grafana provisioning directory in OBSynx lab.
const DefaultOutputDir = "deploy/lab/grafana/dashboards"

// DS Colors
const (
	ColorPrimaryBlue   = "#0056D6"
	ColorElectricCyan  = "#00BFFF"
	ColorPaleIce       = "#E0F7FA"
	ColorDarkCharcoal  = "#333333"
	ColorAlertCrimson  = "#E11D48"
)

// Options holds configuration for dashboard generation.
type Options struct {
	OutputDir string
	Force     bool
}

// GenerateResult summarizes the output of generating dashboards.
type GenerateResult struct {
	GeneratedFiles []string `json:"generated_files"`
	SkippedFiles   []string `json:"skipped_files"`
}

// GenerateFromServices creates Grafana JSON dashboards based on service metrics.
func GenerateFromServices(services []config.ServiceConfig, opts Options) (*GenerateResult, error) {
	outDir := opts.OutputDir
	if strings.TrimSpace(outDir) == "" {
		outDir = DefaultOutputDir
	}

	if err := os.MkdirAll(outDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create dashboard output directory %s: %w", outDir, err)
	}

	result := &GenerateResult{
		GeneratedFiles: make([]string, 0),
		SkippedFiles:   make([]string, 0),
	}

	for _, svc := range services {
		fileName := fmt.Sprintf("obsynx-%s.json", svc.Name)
		targetPath := filepath.Join(outDir, fileName)

		if !opts.Force {
			if _, err := os.Stat(targetPath); err == nil {
				result.SkippedFiles = append(result.SkippedFiles, targetPath)
				continue
			}
		}

		board := BuildDashboard(svc)
		data, err := json.MarshalIndent(board, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("failed to marshal dashboard for %s: %w", svc.Name, err)
		}

		if err := os.WriteFile(targetPath, data, 0644); err != nil {
			return nil, fmt.Errorf("failed to write dashboard file %s: %w", targetPath, err)
		}

		result.GeneratedFiles = append(result.GeneratedFiles, targetPath)
	}

	return result, nil
}

// BuildDashboard constructs a Grafana dashboard structure adhering to OBSYNX Design System.
func BuildDashboard(svc config.ServiceConfig) map[string]interface{} {
	panels := make([]map[string]interface{}, 0)

	// Panel 0: Header Hero
	panels = append(panels, map[string]interface{}{
		"id":      100,
		"title":   "",
		"type":    "text",
		"gridPos": map[string]int{"h": 3, "w": 24, "x": 0, "y": 0},
		"options": map[string]interface{}{
			"mode": "html",
			"content": fmt.Sprintf(`<div style="display: flex; align-items: center; justify-content: space-between; background: #FFFFFF; padding: 14px 24px; border-radius: 10px; border: 1px solid #E2E8F0; box-shadow: 0 4px 16px rgba(0, 86, 214, 0.08); min-height: 70px;">
  <div style="display: flex; align-items: center; gap: 14px;">
    <div style="width: 44px; height: 44px; border-radius: 50%%; background: #0056D6; display: flex; align-items: center; justify-content: center; color: #FFFFFF; font-weight: 700; font-size: 18px;">OX</div>
    <div>
      <div style="display: flex; align-items: center; gap: 10px;">
        <span style="font-size: 20px; font-weight: 700; color: #0056D6; font-family: Inter, sans-serif;">%s</span>
        <span style="color: #0056D6; font-weight: 600; font-size: 11px; padding: 2px 8px; background: #E0F7FA; border-radius: 4px; border: 1px solid #00BFFF; text-transform: uppercase;">%s</span>
      </div>
      <div style="font-size: 12px; color: #333333; margin-top: 2px;">zero-code observability config only • Auto-generated dashboard</div>
    </div>
  </div>
  <div>
    <a href="/d/obsynx-overview" style="text-decoration: none; padding: 6px 14px; background: #0056D6; border-radius: 6px; color: #FFFFFF; font-size: 12px; font-weight: 600;">⬅ Overview</a>
  </div>
</div>`, svc.Name, svc.Runtime),
		},
	})

	yOffset := 3
	panelID := 1

	for _, method := range svc.Instrumentation.Methods {
		for _, m := range method.Metrics {
			p := buildPanelForMetric(panelID, svc.Name, method.Method, m, yOffset)
			panels = append(panels, p)
			panelID++
			yOffset += 7
		}
	}

	return map[string]interface{}{
		"annotations": map[string]interface{}{"list": []interface{}{}},
		"editable":    true,
		"fiscalYearStartMonth": 0,
		"graphTooltip":        1,
		"id":                  nil,
		"title":               fmt.Sprintf("OBSynx — %s", svc.Name),
		"uid":                 fmt.Sprintf("obsynx-%s", svc.Name),
		"schemaVersion":       39,
		"style":               "light",
		"tags":                []string{"obsynx", svc.Runtime, svc.Name},
		"time": map[string]interface{}{
			"from": "now-15m",
			"to":   "now",
		},
		"panels": panels,
	}
}

func buildPanelForMetric(id int, serviceName, methodName string, m config.MethodMetricSpec, y int) map[string]interface{} {
	title := m.Description
	if strings.TrimSpace(title) == "" {
		title = fmt.Sprintf("%s (%s)", m.Name, methodName)
	}

	metricExpr := sanitizeMetricName(m.Name)

	switch m.Type {
	case "gauge":
		return map[string]interface{}{
			"id":    id,
			"title": title,
			"type":  "gauge",
			"gridPos": map[string]int{"h": 7, "w": 12, "x": 0, "y": y},
			"datasource": map[string]string{"type": "prometheus", "uid": "prometheus"},
			"fieldConfig": map[string]interface{}{
				"defaults": map[string]interface{}{
					"color": map[string]string{"mode": "thresholds"},
					"thresholds": map[string]interface{}{
						"mode": "absolute",
						"steps": []map[string]interface{}{
							{"color": ColorElectricCyan, "value": nil},
							{"color": ColorPrimaryBlue, "value": 40},
							{"color": ColorAlertCrimson, "value": 75},
						},
					},
				},
			},
			"targets": []map[string]interface{}{
				{
					"datasource":   map[string]string{"type": "prometheus", "uid": "prometheus"},
					"expr":         fmt.Sprintf(`%s{service_name="%s"}`, metricExpr, serviceName),
					"legendFormat": "{{service_name}}",
					"refId":        "A",
				},
			},
		}

	case "histogram":
		return map[string]interface{}{
			"id":    id,
			"title": title,
			"type":  "timeseries",
			"gridPos": map[string]int{"h": 7, "w": 24, "x": 0, "y": y},
			"datasource": map[string]string{"type": "prometheus", "uid": "prometheus"},
			"fieldConfig": map[string]interface{}{
				"defaults": map[string]interface{}{
					"color": map[string]string{"mode": "fixed", "fixedColor": ColorPrimaryBlue},
					"custom": map[string]interface{}{
						"drawStyle":         "line",
						"lineInterpolation": "smooth",
						"lineWidth":         2.5,
						"fillOpacity":       15,
						"gradientMode":      "opacity",
						"showPoints":        "never",
					},
				},
			},
			"targets": []map[string]interface{}{
				{
					"datasource":   map[string]string{"type": "prometheus", "uid": "prometheus"},
					"expr":         fmt.Sprintf(`rate(%s_sum{service_name="%s"}[1m]) / rate(%s_count{service_name="%s"}[1m])`, metricExpr, serviceName, metricExpr, serviceName),
					"legendFormat": "avg latency (ms)",
					"refId":        "A",
				},
			},
		}

	default: // counter or updowncounter
		return map[string]interface{}{
			"id":    id,
			"title": title,
			"type":  "stat",
			"gridPos": map[string]int{"h": 7, "w": 12, "x": 0, "y": y},
			"datasource": map[string]string{"type": "prometheus", "uid": "prometheus"},
			"fieldConfig": map[string]interface{}{
				"defaults": map[string]interface{}{
					"color": map[string]string{"mode": "fixed", "fixedColor": ColorPrimaryBlue},
					"thresholds": map[string]interface{}{
						"mode": "absolute",
						"steps": []map[string]interface{}{
							{"color": ColorPrimaryBlue, "value": nil},
						},
					},
				},
			},
			"options": map[string]interface{}{
				"colorMode":   "value",
				"graphMode":   "area",
				"justifyMode": "auto",
				"textMode":    "value_and_name",
			},
			"targets": []map[string]interface{}{
				{
					"datasource":   map[string]string{"type": "prometheus", "uid": "prometheus"},
					"expr":         fmt.Sprintf(`sum(increase(%s{service_name="%s"}[1m]))`, metricExpr, serviceName),
					"legendFormat": "{{service_name}}",
					"refId":        "A",
				},
			},
		}
	}
}

func sanitizeMetricName(name string) string {
	clean := strings.ReplaceAll(name, ".", "_")
	clean = strings.ReplaceAll(clean, "-", "_")
	return clean
}
