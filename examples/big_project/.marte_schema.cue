package schema

#Classes: {
	PulseDetectorGAM: {
		UpperLimit!: number
		LowerLimit!: number
		...
		OutputSginals: {
			[_]: {
				Type: "uint8"
				...
			}
		}
	}
	SystemClockGAM: {
		OutputSignals: {
			Time: {
				Type: "uint64"
				...
			}
		}
	}
	NI6683H: {
		BoardId: uint
		Signals: {
			Status: {
				Type: "uint8"
				...
			}
			PtpdStatus: {
				Type: "uint8"
				...
			}
			Time: {
				Type: "uint64"
				...
			}
		}
	}
	SystemClockTimeProvider: {...}
	TriggerEnableGAM: {
		NumberOfTriggers!: uint & >0
		ResetCycles!:      uint & >0
		InputSignals: {
			...
		}
		OutputSignals: {
			...
		}
	}
	MathExpressionExprtkGAM: {
		...
		OutputSignals: {
			[_]: {
				Expression: string
				...
			}
		}
	}
	ScaleGAM: {
		InputSignals: {
			[_]: {
				Factor: number
				...
			}
		}
		...
	}
	UDPStreamer: {
		Port!:          uint & >1024 & <49151
		MaxPayloadSize: uint
		Signals: {
			[_]: {
				Unit?:    string
				TimeMode: "FirstSample" | "LastSample" | *"Packet" | "FullArray"
				if TimeMode != "Packet" {
					TimeSignal!: string
				}
				RangeMin?:     float
				RangeMax?:     float
				SamplingRage?: float
				...
			}
		}
	}
	DebugService: {
		ControlPort!: uint & >1024 & <49151
		UdpPort!:     uint & >1024 & <49151
		LogPort!:     uint & >1024 & <49151
		StreamIP:     string
	}
	ExtractBitGAM: {...}
	CompactBitGAM: {...}
	HttpService: {
		Port!:                uint & >1024 & <49151
		WebRoot?:             string
		Timeout?:             uint
		ListenMaxConnection?: uint
		AcceptTimeout?:       uint
		MaxNumberOfThreads?:  uint
		MinNumberOfThreads?:  uint
	}

	HttpObjectBrowser: {
		Root!: string
		...
	}
	HttpDataMonitor: {
		...
	}
	HttpObjectBrowser: {
		Root!: string
	}
	HttpDirectoryResource: {
		BaseDir: string
	}
	HttpMessageInterface: {
		...
	}
	NI6528: {
		...
	}
	ConfigurationDatabase: {
		...
	}
	JAWFRecordGAM: {
		Directory!: string
		...
	}
	ScaleGAM: {
		InputSignals: {
			[_]: {
				Factor: number
				...
			}
		}
		OutputSignals: {
			[_]: {
				...
			}
		}
		...
	}
	JAPreProgrammedGAM: {
		Directory!:             string
		PreProgrammedPeriodMs!: uint32
		...
	}
	JAConditionalSignalUpdateGAM: {
		Operation!: "AND" | "OR" | "XOR" | "NOR"
		InputSignals: {
			[_]: {
				Comparator!: "EQUALS" | "GREATER" | "LESSER"
				Value!:      number
				Type!:       string
				...
			}
		}
		OutputSignals: {
			[_]: {
				DefaultValue!: uint32
				Value!:        uint32
				Type!:         string
				...
			}
		}
		...
	}
	JASourceChoiceGAM: {
		...
	}
	JAMessageGAM: {
		Operation!: "AND" | "OR"
		InputSignals: {
			[_]: {
				Value!:      number
				Comparator!: "EQUALS" | "GREATER" | "LESS" | "EQUALS_OR_GREATER" | "EQUALS_OR_LESS" | "NOT"
				Type!:       string
				...
			}
		}
		Event!: {
			Class:        "Message"
			Destination!: string
			Function!:    string
		}
		...
	}
	JAModeControlGAM: {
		...
	}
	JARTStateMachineGAM: {
		ConditionTrigger: 0 | 1
		mhvps_hvon:       uint
		aps_hvon:         uint
		aps_swon:         uint
		bps_hvon:         uint
		bps_swon:         uint
		...
	}
	JASDNRTStateMachineGAM: {
		ConditionTrigger: 0 | 1
		mhvps_hvon:       uint
		aps_hvon:         uint
		aps_swon:         uint
		bps_hvon:         uint
		bps_swon:         uint
		...
	}
	DANSource: {
		...
	}
	JATriangleWaveGAM: {
		...
	}
	JARampupGAM: {
		CycleFrequency?: float
		RampDownRate?:   float
		InputSignals: {
			Command!: {
				Type: "uint32"
				...
			}
			RampSetPoint!: {
				Type?: "float32"
				...
			}
			RampRate!: {
				Type?: "float32"
				...
			}
			ManualMode!: {
				Type?: "uint32"
				...
			}
			ManualSetPoint!: {
				Type?: "float32"
				...
			}
			PLCStandbyState!: {
				Type?: "uint8"
				...
			}
		}
		OutputSignals: {
			Reference!: {
				Type?: "float32"
				...
			}
			State!: {
				Type?: "uint32"
				...
			}
		}
	}
	NI6683H: {
		BoardId: uint
		...
	}
}
