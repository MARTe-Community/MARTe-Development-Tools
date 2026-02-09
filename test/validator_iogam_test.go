package integration

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

func TestValidator_IOGAM(t *testing.T) {
	// Common boilerplate for valid RealTimeApplication
	commonAppStart := `
	+TDS = { Class = TimingDataSource }
	+App = {
		Class = RealTimeApplication
		States = { Class = ReferenceContainer }
		Scheduler = { Class = GAMScheduler TimingDataSource = TDS }
	`

	tests := []struct {
		name     string
		src      string
		expected []string
	}{
		{
			name: "Correct IOGAM size",
			src: commonAppStart + `
				Data = { Class = ReferenceContainer DefaultDataSource = TDS }
				+Functions = {
					Class = ReferenceContainer
					+MyIOGAM = {
						Class = IOGAM
						InputSignals = {
							Sig1 = {
								Type = uint32
								NumberOfElements = 2
							}
						}
						OutputSignals = {
							Sig2 = {
								Type = uint64
							}
						}
					}
				}
			}`,
			expected: []string{},
		},
		{
			name: "Incorrect IOGAM size",
			src: commonAppStart + `
				Data = { Class = ReferenceContainer DefaultDataSource = TDS }
				+Functions = {
					Class = ReferenceContainer
					//! ignore(unused)
					+MyIOGAM = {
						Class = IOGAM
						InputSignals = {
							Sig1 = { Type = uint32 }
						}
						OutputSignals = {
							Sig2 = { Type = uint64 }
						}
					}
				}
			}`,
			expected: []string{"Schema Validation Error"},
		},
		{
			name: "IOGAM with DataSource Reference",
			src: commonAppStart + `
				Data = {
					Class = ReferenceContainer
					DefaultDataSource = TDS
					+DS = {
						Class = DataSource
						Signals = {
							DSSig1 = { Type = uint32 NumberOfElements = 10 }
							DSSig2 = { Type = uint32 NumberOfElements = 10 }
						}
					}
				}
				+Functions = {
					Class = ReferenceContainer
					+MyIOGAM = {
						Class = IOGAM
						InputSignals = {
							Sig1 = {
								DataSource = DS
								Alias = DSSig1
							}
						}
						OutputSignals = {
							Sig2 = {
								DataSource = DS
								Alias = DSSig2
							}
						}
					}
				}
			}`,
			expected: []string{},
		},
		{
			name: "IOGAM with Ranges",
			src: commonAppStart + `
				Data = { Class = ReferenceContainer DefaultDataSource = TDS }
				+Functions = {
					Class = ReferenceContainer
					+MyIOGAM = {
						Class = IOGAM
						InputSignals = {
							Sig1 = {
								Type = uint32
								NumberOfElements = 100
								Ranges = { { 0 9 } }
							}
						}
						OutputSignals = {
							Sig2 = {
								Type = uint64
								NumberOfElements = 5
							}
						}
					}
				}
			}`,
			expected: []string{},
		},
		{
			name: "IOGAM with Samples",
			src: commonAppStart + `
				Data = { Class = ReferenceContainer DefaultDataSource = TDS }
				+Functions = {
					Class = ReferenceContainer
					+MyIOGAM = {
						Class = IOGAM
						InputSignals = {
							Sig1 = {
								Type = uint32
								Samples = 10
							}
						}
						OutputSignals = {
							Sig2 = {
								Type = uint64
								NumberOfElements = 5
							}
						}
					}
				}
			}`,
			expected: []string{},
		},
		{
			name: "IOGAM with Ranges and Samples",
			src: commonAppStart + `
				Data = { Class = ReferenceContainer DefaultDataSource = TDS }
				+Functions = {
					Class = ReferenceContainer
					+MyIOGAM = {
						Class = IOGAM
						InputSignals = {
							Sig1 = {
								Type = uint32
								Ranges = { { 0 4 } }
								Samples = 2
							}
						}
						OutputSignals = {
							Sig2 = {
								Type = float64
								NumberOfElements = 5
							}
						}
					}
				}
			}`,
			expected: []string{},
		},
		{
			name: "IOGAM with Variables in Ranges and Samples",
			src: `
			#let START: uint32 = 0
			#let STOP: uint32 = 9
			#let SAMPLES: uint32 = 2
			` + commonAppStart + `
				Data = { Class = ReferenceContainer DefaultDataSource = TDS }
				+Functions = {
					Class = ReferenceContainer
					+MyIOGAM = {
						Class = IOGAM
						InputSignals = {
							Sig1 = {
								Type = uint32
								Ranges = { { @START @STOP } }
								Samples = @SAMPLES * 5
							}
						}
						OutputSignals = {
							Sig2 = {
								Type = uint64
								NumberOfElements = 50
							}
						}
					}
				}
			}`,
			expected: []string{},
		},
		{
			name: "IOGAM with Variable in DataSource Signal",
			src: `
			#let DAN_RATIO: uint32 = 1000
			` + commonAppStart + `
				Data = {
					Class = ReferenceContainer
					DefaultDataSource = TDS
					+DS = {
						Class = DataSource
						Signals = {
							DSSig1 = { Type = uint32 NumberOfElements = @DAN_RATIO } // 4 * 1000 = 4000
						}
					}
				}
				+Functions = {
					Class = ReferenceContainer
					+MyIOGAM = {
						Class = IOGAM
						InputSignals = {
							Sig1 = {
								DataSource = DS
								Alias = DSSig1
							} // 4000
						}
						OutputSignals = {
							Sig2 = {
								Type = uint32
								NumberOfElements = 1000
							} // 4000
						}
					}
				}
			}`,
			expected: []string{},
		},
		{
			name: "Ranges Modifier Suppresses Elements Mismatch",
			src: commonAppStart + `
				Data = {
					Class = ReferenceContainer
					DefaultDataSource = TDS
					+DS = {
						Class = DataSource
						Signals = {
							Sig1 = { Type = uint32 NumberOfElements = 100 }
						}
					}
				}
				+Functions = {
					Class = ReferenceContainer
					+MyGAM = {
						Class = IOGAM
						InputSignals = {
							Sig1 = {
								DataSource = DS
								Ranges = { { 0 9 } }
								NumberOfElements = 10
							}
						}
						OutputSignals = {
							Sig2 = {
								Type = uint32
								NumberOfElements = 10
							}
						}
					}
				}
			}`,
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pt := index.NewProjectTree()
			p := parser.NewParser(tt.src)
			cfg, err := p.Parse()
			if err != nil {
				t.Fatalf("Parse failed: %v", err)
			}
			pt.AddFile("test.marte", cfg)
			
			pt.ResolveReferences()
			pt.ResolveFields()

			v := validator.NewValidator(pt, ".", nil)
			v.ValidateProject()

			for _, exp := range tt.expected {
				found := false
				for _, d := range v.Diagnostics {
					if strings.Contains(d.Message, exp) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected error containing '%s', but got: %v", exp, v.Diagnostics)
				}
			}

			if len(tt.expected) == 0 {
				for _, d := range v.Diagnostics {
					if strings.Contains(d.Message, "IOGAM Size Mismatch") || strings.Contains(d.Message, "Schema Validation Error") {
						t.Errorf("Unexpected IOGAM error: %s", d.Message)
					}
				}
			}
		})
	}
}
