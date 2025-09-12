package main

import (
	"fmt"
	"html/template"
	"os"
	"time"
)

// Generate HTML report
func (t *MongoTester) generateHTMLReport() error {
	htmlTemplate := `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>MongoDB Comprehensive Test Report - {{.DatabaseName}}</title>
    <style>
        * { box-sizing: border-box; }
        body { 
            font-family: 'Segoe UI', Tahoma, Geneva, Verdana, sans-serif; 
            margin: 0; padding: 0; background: #f8fafc; 
            line-height: 1.6; color: #2d3748;
        }
        
        .container { max-width: 1200px; margin: 0 auto; padding: 20px; }
        
        .header { 
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%); 
            color: white; padding: 40px; border-radius: 12px; 
            text-align: center; margin-bottom: 30px; box-shadow: 0 8px 32px rgba(0,0,0,0.1);
        }
        .header h1 { margin: 0; font-size: 3em; font-weight: 700; }
        .header .subtitle { margin: 15px 0 0 0; opacity: 0.9; font-size: 1.2em; }
        .header .meta { margin: 10px 0 0 0; opacity: 0.8; font-size: 1em; }
        
        .stats-grid { 
            display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); 
            gap: 20px; margin: 30px 0; 
        }
        .stat-card { 
            background: white; padding: 25px; border-radius: 12px; text-align: center; 
            box-shadow: 0 4px 20px rgba(0,0,0,0.08); transition: transform 0.2s ease;
        }
        .stat-card:hover { transform: translateY(-2px); }
        .stat-number { font-size: 2.5em; font-weight: bold; margin-bottom: 5px; }
        .stat-label { color: #718096; font-size: 1.1em; }
        .success-number { color: #38a169; }
        .error-number { color: #e53e3e; }
        .total-number { color: #3182ce; }
        .duration-number { color: #805ad5; }
        
        .section { 
            background: white; margin: 30px 0; padding: 35px; border-radius: 12px; 
            box-shadow: 0 4px 20px rgba(0,0,0,0.08); 
        }
        .section h2 { 
            margin: 0 0 25px 0; color: #2d3748; font-size: 1.8em; font-weight: 600;
            border-bottom: 3px solid #e2e8f0; padding-bottom: 15px; 
        }
        
        .category-grid { 
            display: grid; grid-template-columns: repeat(auto-fit, minmax(300px, 1fr)); 
            gap: 20px; margin: 25px 0; 
        }
        .category-card { 
            background: #f7fafc; padding: 25px; border-radius: 8px; 
            border-left: 5px solid #4299e1; position: relative;
        }
        .category-name { font-weight: 600; font-size: 1.3em; margin-bottom: 15px; color: #2d3748; }
        .category-stats { font-size: 1.1em; margin-bottom: 15px; color: #4a5568; }
        .progress-bar { 
            background: #e2e8f0; height: 12px; border-radius: 6px; 
            overflow: hidden; position: relative; 
        }
        .progress-fill { 
            background: linear-gradient(90deg, #38a169, #48bb78); 
            height: 100%; transition: width 0.8s ease; border-radius: 6px;
        }
        .progress-text { 
            position: absolute; top: 50%; left: 50%; transform: translate(-50%, -50%); 
            font-size: 0.9em; font-weight: 600; color: white; text-shadow: 0 1px 2px rgba(0,0,0,0.3);
        }
        
        .filters { 
            margin: 20px 0; display: flex; gap: 10px; flex-wrap: wrap; align-items: center; 
        }
        .filter-btn { 
            padding: 8px 16px; border: 2px solid #e2e8f0; background: white; 
            border-radius: 20px; cursor: pointer; transition: all 0.2s ease; font-size: 0.9em;
        }
        .filter-btn:hover, .filter-btn.active { 
            background: #4299e1; color: white; border-color: #4299e1; 
        }
        .search-box { 
            padding: 10px 15px; border: 2px solid #e2e8f0; border-radius: 8px; 
            font-size: 1em; width: 250px; 
        }
        .search-box:focus { outline: none; border-color: #4299e1; }
        
        .test-grid { display: grid; gap: 20px; margin-top: 25px; }
        .test-item { 
            padding: 25px; border: 2px solid #e2e8f0; border-radius: 10px; 
            transition: all 0.3s ease; position: relative; background: white;
        }
        .test-item:hover { box-shadow: 0 8px 25px rgba(0,0,0,0.1); transform: translateY(-1px); }
        .test-item.success { border-left: 6px solid #38a169; }
        .test-item.failed { border-left: 6px solid #e53e3e; }
        
        .test-header { display: flex; justify-content: between; align-items: flex-start; gap: 15px; }
        .test-name { font-weight: 700; color: #2d3748; font-size: 1.3em; flex: 1; }
        .test-status { font-size: 1.5em; }
        .test-meta { 
            display: grid; grid-template-columns: repeat(auto-fit, minmax(150px, 1fr)); 
            gap: 15px; margin: 15px 0; font-size: 0.95em; color: #718096; 
        }
        .meta-item { 
            display: flex; align-items: center; gap: 8px; padding: 8px 12px; 
            background: #f7fafc; border-radius: 6px; 
        }
        .meta-label { font-weight: 600; }
        .test-description { 
            color: #4a5568; margin: 15px 0; font-size: 1.05em; 
            padding: 15px; background: #f7fafc; border-radius: 8px; border-left: 4px solid #4299e1;
        }
        
        .test-error { 
            background: #fed7d7; border: 2px solid #fc8181; padding: 20px; 
            border-radius: 8px; margin-top: 20px; position: relative;
        }
        .error-header { 
            font-weight: 600; color: #c53030; margin-bottom: 10px; 
            display: flex; align-items: center; gap: 8px; font-size: 1.1em;
        }
        .error-message { 
            font-family: 'Consolas', 'Monaco', 'Courier New', monospace; 
            font-size: 0.9em; color: #742a2a; white-space: pre-wrap; 
            word-break: break-word; background: white; padding: 15px; 
            border-radius: 6px; margin-top: 10px;
        }
        
        .test-details { margin-top: 20px; }
        .detail-section { 
            background: #f7fafc; padding: 15px; margin: 10px 0; 
            border-radius: 8px; border-left: 4px solid #4299e1; 
        }
        .detail-title { font-weight: 600; color: #2d3748; margin-bottom: 8px; }
        .detail-content { color: #4a5568; font-family: 'Consolas', monospace; font-size: 0.9em; }
        
        .summary-section { 
            background: linear-gradient(135deg, #667eea20, #764ba220); 
            border-radius: 12px; padding: 30px; margin: 30px 0; 
        }
        .summary-grid { 
            display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); 
            gap: 20px; margin-top: 20px; 
        }
        .summary-item { text-align: center; }
        .summary-number { font-size: 2em; font-weight: bold; }
        .summary-label { color: #4a5568; margin-top: 5px; }
        
        .execution-timeline { margin: 25px 0; }
        .timeline-item { 
            display: flex; align-items: center; gap: 15px; padding: 12px 0; 
            border-bottom: 1px solid #e2e8f0; 
        }
        .timeline-time { 
            font-family: monospace; background: #f7fafc; padding: 6px 10px; 
            border-radius: 4px; min-width: 100px; text-align: center; font-size: 0.9em;
        }
        .timeline-status { font-size: 1.2em; }
        .timeline-name { font-weight: 500; color: #2d3748; }
        .timeline-duration { color: #718096; font-size: 0.9em; margin-left: auto; }
        
        .collapsible { cursor: pointer; user-select: none; }
        .collapsible:hover { background: #f7fafc; }
        .collapsible::after { 
            content: '+'; float: right; font-weight: bold; 
            transition: transform 0.2s; font-size: 1.2em; 
        }
        .collapsible.active::after { transform: rotate(45deg); }
        .collapsible-content { 
            max-height: 0; overflow: hidden; transition: max-height 0.3s ease; 
        }
        .collapsible-content.active { max-height: 1000px; }
        
        .footer { 
            text-align: center; color: #718096; margin: 50px 0 30px; 
            padding: 30px; border-top: 2px solid #e2e8f0; 
        }
        .footer h3 { color: #2d3748; margin-bottom: 15px; }
        
        .hidden { display: none !important; }
        
        @media (max-width: 768px) {
            .container { padding: 10px; }
            .header { padding: 25px; }
            .header h1 { font-size: 2em; }
            .stats-grid { grid-template-columns: repeat(2, 1fr); }
            .category-grid { grid-template-columns: 1fr; }
            .test-meta { grid-template-columns: 1fr; }
            .search-box { width: 100%; margin-bottom: 10px; }
        }
        
        /* Print styles */
        @media print {
            body { background: white; }
            .header { background: #667eea !important; }
            .section, .stat-card, .category-card, .test-item { 
                box-shadow: none !important; border: 1px solid #e2e8f0 !important; 
            }
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>MongoDB Comprehensive Test Report</h1>
            <div class="subtitle">{{.DatabaseName}} | {{.TotalTests}} Test Operations</div>
            <div class="meta">
                Generated: {{.EndTime.Format "Monday, January 2, 2006 at 3:04:05 PM MST"}} | 
                Duration: {{.TotalDuration}} | 
                Connection: {{.ConnectionString}}
            </div>
        </div>

        <div class="stats-grid">
            <div class="stat-card">
                <div class="stat-number success-number">{{.PassedTests}}</div>
                <div class="stat-label">Tests Passed</div>
            </div>
            <div class="stat-card">
                <div class="stat-number error-number">{{.FailedTests}}</div>
                <div class="stat-label">Tests Failed</div>
            </div>
            <div class="stat-card">
                <div class="stat-number total-number">{{.TotalTests}}</div>
                <div class="stat-label">Total Tests</div>
            </div>
            <div class="stat-card">
                <div class="stat-number duration-number">{{printf "%.1f%%" (div (mul (itof .PassedTests) 100.0) (itof .TotalTests))}}</div>
                <div class="stat-label">Success Rate</div>
            </div>
        </div>

        <div class="section">
            <h2>Test Execution Summary</h2>
            <div class="summary-section">
                <div class="summary-grid">
                    <div class="summary-item">
                        <div class="summary-number success-number">{{printf "%.2f" (averageDuration .Results)}}ms</div>
                        <div class="summary-label">Average Duration</div>
                    </div>
                    <div class="summary-item">
                        <div class="summary-number total-number">{{fastestTest .Results}}ms</div>
                        <div class="summary-label">Fastest Test</div>
                    </div>
                    <div class="summary-item">
                        <div class="summary-number duration-number">{{slowestTest .Results}}ms</div>
                        <div class="summary-label">Slowest Test</div>
                    </div>
                    <div class="summary-item">
                        <div class="summary-number">{{len (groupByCategory .Results)}}</div>
                        <div class="summary-label">Categories</div>
                    </div>
                </div>
            </div>
        </div>

        <div class="section">
            <h2>Category Performance Overview</h2>
            <div class="category-grid">
                {{range $category, $tests := groupByCategory .Results}}
                <div class="category-card">
                    <div class="category-name">{{$category}}</div>
                    <div class="category-stats">
                        {{countPassed $tests}}/{{len $tests}} passed 
                        ({{printf "%.1f%%" (div (mul (itof (countPassed $tests)) 100.0) (itof (len $tests)))}})
                    </div>
                    <div class="progress-bar">
                        <div class="progress-fill" style="width: {{div (mul (itof (countPassed $tests)) 100.0) (itof (len $tests))}}%"></div>
                        <div class="progress-text">{{printf "%.0f%%" (div (mul (itof (countPassed $tests)) 100.0) (itof (len $tests)))}}</div>
                    </div>
                    <div style="margin-top: 15px; font-size: 0.9em; color: #718096;">
                        Avg Duration: {{printf "%.2f" (categoryAverageDuration $tests)}}ms
                        {{if gt (countFailed $tests) 0}}
                        <br><span style="color: #e53e3e;">{{countFailed $tests}} failures</span>
                        {{end}}
                    </div>
                </div>
                {{end}}
            </div>
        </div>

        {{$failedTests := getFailedTests .Results}}
        {{if $failedTests}}
        <div class="section">
            <h2>Failed Tests Analysis ({{len $failedTests}})</h2>
            <div class="test-grid">
                {{range $failedTests}}
                <div class="test-item failed">
                    <div class="test-header">
                        <div class="test-name">{{.Name}}</div>
                        <div class="test-status">❌</div>
                    </div>
                    <div class="test-meta">
                        <div class="meta-item">
                            <span class="meta-label">Category:</span> {{.Category}}
                        </div>
                        <div class="meta-item">
                            <span class="meta-label">Duration:</span> {{printf "%.2f" (div (itof .Duration.Nanoseconds) 1000000.0)}}ms
                        </div>
                        <div class="meta-item">
                            <span class="meta-label">Expected:</span> {{.ExpectedType}}
                        </div>
                        <div class="meta-item">
                            <span class="meta-label">Test ID:</span> #{{.ID}}
                        </div>
                    </div>
                    <div class="test-description">{{.Description}}</div>
                    <div class="test-error">
                        <div class="error-header">
                            🚨 Error Details
                        </div>
                        <div class="error-message">{{.Error}}</div>
                    </div>
                </div>
                {{end}}
            </div>
        </div>
        {{end}}

        <div class="section">
            <h2>All Test Results</h2>
            <div class="filters">
                <input type="text" class="search-box" placeholder="Search tests..." id="searchBox">
                <button class="filter-btn" data-filter="all">All Tests</button>
                <button class="filter-btn" data-filter="passed">Passed Only</button>
                <button class="filter-btn active" data-filter="failed">Failed Only</button>
                {{range $category, $tests := groupByCategory .Results}}
                <button class="filter-btn" data-filter="{{$category}}">{{$category}}</button>
                {{end}}
            </div>
            
            <div class="test-grid" id="testGrid">
                {{range .Results}}
                <div class="test-item {{if .Success}}success{{else}}failed{{end}}" 
                     data-category="{{.Category}}" data-status="{{if .Success}}passed{{else}}failed{{end}}">
                    <div class="test-header">
                        <div class="test-name">{{.Name}}</div>
                        <div class="test-status">{{if .Success}}✅{{else}}❌{{end}}</div>
                    </div>
                    <div class="test-meta">
                        <div class="meta-item">
                            <span class="meta-label">Category:</span> {{.Category}}
                        </div>
                        <div class="meta-item">
                            <span class="meta-label">Duration:</span> {{printf "%.2f" (div (itof .Duration.Nanoseconds) 1000000.0)}}ms
                        </div>
                        <div class="meta-item">
                            <span class="meta-label">Expected:</span> {{.ExpectedType}}
                        </div>
                        <div class="meta-item">
                            <span class="meta-label">Test ID:</span> #{{.ID}}
                        </div>
                        <div class="meta-item">
                            <span class="meta-label">Timestamp:</span> {{.Timestamp.Format "15:04:05"}}
                        </div>
                        {{if .DocumentCount}}
                        <div class="meta-item">
                            <span class="meta-label">Documents:</span> {{.DocumentCount}}
                        </div>
                        {{end}}
                    </div>
                    <div class="test-description">{{.Description}}</div>
                    {{if not .Success}}
                    <div class="test-error">
                        <div class="error-header">
                            🚨 Error Details
                        </div>
                        <div class="error-message">{{.Error}}</div>
                    </div>
                    {{else}}
                    <div class="test-details">
                        <div class="detail-section">
                            <div class="detail-title">✅ Test Completed Successfully</div>
                            <div class="detail-content">
                                Expected return type: {{.ExpectedType}}<br>
                                Execution time: {{printf "%.3f" (div (itof .Duration.Nanoseconds) 1000000.0)}}ms<br>
                                Status: PASSED
                            </div>
                        </div>
                    </div>
                    {{end}}
                </div>
                {{end}}
            </div>
        </div>

        <div class="footer">
            <h3>Test Suite Information</h3>
            <p><strong>MongoBouncer Comprehensive Test Suite</strong></p>
            <p>This report validates MongoBouncer's handling of {{.TotalTests}} MongoDB operations across {{len (groupByCategory .Results)}} categories</p>
            <p>Report generated on {{.EndTime.Format "January 2, 2006"}} at {{.EndTime.Format "3:04:05 PM MST"}}</p>
            <p><em>Test data has been automatically cleaned up after execution</em></p>
        </div>
    </div>

    <script>
        // Filter functionality
        document.addEventListener('DOMContentLoaded', function() {
            const searchBox = document.getElementById('searchBox');
            const filterButtons = document.querySelectorAll('.filter-btn');
            const testItems = document.querySelectorAll('#testGrid .test-item');
            
            let currentFilter = 'all';
            let currentSearch = '';
            
            function applyFilters() {
                testItems.forEach(item => {
                    const category = item.dataset.category.toLowerCase();
                    const status = item.dataset.status;
                    const testName = item.querySelector('.test-name').textContent.toLowerCase();
                    const testDescription = item.querySelector('.test-description').textContent.toLowerCase();
                    
                    const matchesFilter = currentFilter === 'all' || 
                                        currentFilter === status || 
                                        currentFilter.toLowerCase() === category;
                    
                    const matchesSearch = currentSearch === '' || 
                                        testName.includes(currentSearch.toLowerCase()) ||
                                        testDescription.includes(currentSearch.toLowerCase()) ||
                                        category.includes(currentSearch.toLowerCase());
                    
                    if (matchesFilter && matchesSearch) {
                        item.classList.remove('hidden');
                    } else {
                        item.classList.add('hidden');
                    }
                });
                
                updateResultCount();
            }
            
            function updateResultCount() {
                const visibleItems = document.querySelectorAll('#testGrid .test-item:not(.hidden)');
                const totalItems = testItems.length;
                console.log("Showing ${visibleItems.length} of ${totalItems} tests");
            }
            
            searchBox.addEventListener('input', function(e) {
                currentSearch = e.target.value;
                applyFilters();
            });
            
            filterButtons.forEach(button => {
                button.addEventListener('click', function() {
                    filterButtons.forEach(btn => btn.classList.remove('active'));
                    this.classList.add('active');
                    currentFilter = this.dataset.filter;
                    applyFilters();
                });
            });
        });
        
        // Collapsible sections
        function toggleCollapsible(element) {
            element.classList.toggle('active');
            const content = element.nextElementSibling;
            content.classList.toggle('active');
        }
        
        // Auto-scroll to failed tests if any exist
        document.addEventListener('DOMContentLoaded', function() {
            const failedSection = document.querySelector('.section h2');
            if (failedSection && failedSection.textContent.includes('Failed Tests')) {
                setTimeout(() => {
                    failedSection.scrollIntoView({ behavior: 'smooth', block: 'start' });
                }, 1000);
            }
        });
    </script>
</body>
</html>`

	// Template functions remain the same as before...
	funcMap := template.FuncMap{
		"div": func(a, b interface{}) float64 {
			aVal, bVal := 0.0, 0.0
			switch v := a.(type) {
			case int:
				aVal = float64(v)
			case int64:
				aVal = float64(v)
			case float64:
				aVal = v
			}
			switch v := b.(type) {
			case int:
				bVal = float64(v)
			case int64:
				bVal = float64(v)
			case float64:
				bVal = v
			}
			if bVal == 0 {
				return 0
			}
			return aVal / bVal
		},
		"mul": func(a, b interface{}) float64 {
			aVal, bVal := 0.0, 0.0
			switch v := a.(type) {
			case int:
				aVal = float64(v)
			case int64:
				aVal = float64(v)
			case float64:
				aVal = v
			}
			switch v := b.(type) {
			case int:
				bVal = float64(v)
			case int64:
				bVal = float64(v)
			case float64:
				bVal = v
			}
			return aVal * bVal
		},
		"itof": func(i interface{}) float64 {
			switch v := i.(type) {
			case int:
				return float64(v)
			case int64:
				return float64(v)
			case float64:
				return v
			default:
				return 0
			}
		},
		"groupByCategory": func(results []TestResult) map[string][]TestResult {
			groups := make(map[string][]TestResult)
			for _, result := range results {
				groups[result.Category] = append(groups[result.Category], result)
			}
			return groups
		},
		"countPassed": func(results []TestResult) int {
			count := 0
			for _, result := range results {
				if result.Success {
					count++
				}
			}
			return count
		},
		"countFailed": func(results []TestResult) int {
			count := 0
			for _, result := range results {
				if !result.Success {
					count++
				}
			}
			return count
		},
		"getFailedTests": func(results []TestResult) []TestResult {
			var failed []TestResult
			for _, result := range results {
				if !result.Success {
					failed = append(failed, result)
				}
			}
			return failed
		},
		"averageDuration": func(results []TestResult) float64 {
			if len(results) == 0 {
				return 0
			}
			total := int64(0)
			for _, result := range results {
				total += result.Duration.Nanoseconds()
			}
			return float64(total) / float64(len(results)) / 1000000.0 // Convert to milliseconds
		},
		"fastestTest": func(results []TestResult) float64 {
			if len(results) == 0 {
				return 0
			}
			fastest := results[0].Duration.Nanoseconds()
			for _, result := range results {
				if result.Duration.Nanoseconds() < fastest {
					fastest = result.Duration.Nanoseconds()
				}
			}
			return float64(fastest) / 1000000.0
		},
		"slowestTest": func(results []TestResult) float64 {
			if len(results) == 0 {
				return 0
			}
			slowest := results[0].Duration.Nanoseconds()
			for _, result := range results {
				if result.Duration.Nanoseconds() > slowest {
					slowest = result.Duration.Nanoseconds()
				}
			}
			return float64(slowest) / 1000000.0
		},
		"categoryAverageDuration": func(results []TestResult) float64 {
			if len(results) == 0 {
				return 0
			}
			total := int64(0)
			for _, result := range results {
				total += result.Duration.Nanoseconds()
			}
			return float64(total) / float64(len(results)) / 1000000.0
		},
	}

	tmpl, err := template.New("report").Funcs(funcMap).Parse(htmlTemplate)
	if err != nil {
		return fmt.Errorf("failed to parse template: %w", err)
	}

	filename := fmt.Sprintf("mongodb_test_report_%s.html", time.Now().Format("20060102_150405"))
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("failed to create HTML file: %w", err)
	}
	defer file.Close()

	if err := tmpl.Execute(file, t.testSuite); err != nil {
		return fmt.Errorf("failed to execute template: %w", err)
	}

	fmt.Printf("📄 Comprehensive HTML report generated: %s\n", filename)
	return nil
}
