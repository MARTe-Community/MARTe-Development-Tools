package schema

#Classes: {
	RealTimeApplication: {
		Functions!: {
			Class: "ReferenceContainer"
			[_= !~"^Class$"]: {
				#meta: type: "gam"
				...
			}
		} // type: node
		Data!: {
			Class:             "ReferenceContainer"
			DefaultDataSource: string
			[_= !~"^(Class|DefaultDataSource)$"]: {
				#meta: type: "datasource"
				...
			}
		}
		States!: {
			Class: "ReferenceContainer"
			[_= !~"^Class$"]: {
				Class: "RealTimeState"
				...
			}
		} // type: node
		Scheduler!: {
			...
			#meta: type: "scheduler"
		}
		...
	}
	Message: {
		...
	}
	StateMachineEvent: {
		NextState!:                                                  string
		NextStateError!:                                             string
		Timeout?:                                                    uint32
		[_= !~"^(Class|NextState|Timeout|NextStateError|[#_$].+)$"]: Message
		...
	}
	_State: {
		Class: "ReferenceContainer"
		ENTER?: {
			Class: "ReferenceContainer"
			...
		}
		[_ = !~"^(Class|ENTER|EXIT)$"]: StateMachineEvent
		...
	}
	StateMachine: {
		[_ = !~"^(Class|[$].*)$"]: _State
		...
	}
	RealTimeState: {
		Threads: {...} // type: node
		...
	}
	RealTimeThread: {
		Functions: [...] // type: array
		...
	}
	GAMScheduler: {
		TimingDataSource: string // type: reference
		#meta: type: "scheduler"
		...
	}
	TimingDataSource: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "IN"
		#meta: type:          "datasource"
		...
	}
	IOGAM: {
		InputSignals?: {...} // type: node
		OutputSignals?: {...} // type: node
		#meta: type: "gam"
		...
	}
	ReferenceContainer: {
		...
	}
	ConstantGAM: {
		...
		#meta: type: "gam"
	}
	PIDGAM: {
		Kp: float | int // type: float (allow int as it promotes)
		Ki: float | int
		Kd: float | int
		#meta: type: "gam"
		...
	}
	FileDataSource: {
		Filename: string
		Format?:  string
		#meta: multithreaded: bool | *false
		#meta: direction:     "INOUT"
		#meta: type:          "datasource"
		...
	}
	LoggerDataSource: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "OUT"
		#meta: type:          "datasource"
		...
	}
	DANStream: {
		Timeout?: int
		#meta: multithreaded: bool | *false
		#meta: direction:     "OUT"
		#meta: type:          "datasource"
		...
	}
	EPICSCAInput: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "IN"
		#meta: type:          "datasource"
		...
	}
	EPICSCAOutput: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "OUT"
		#meta: type:          "datasource"
		...
	}
	EPICSPVAInput: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "IN"
		#meta: type:          "datasource"
		...
	}
	EPICSPVAOutput: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "OUT"
		#meta: type:          "datasource"
		...
	}
	SDNSubscriber: {
		ExecutionMode?:      *"IndependentThread" | "RealTimeThread"
		Topic!:              string
		Address?:            string
		Interface!:          string
		CPUs?:               uint32
		InternalTimeout?:    uint32
		Timeout?:            uint32
		IgnoreTimeoutError?: 0 | 1
		#meta: multithreaded: bool | *false
		#meta: direction:     "IN"
		#meta: type:          "datasource"
		...
	}
	SDNPublisher: {
		Address:    string
		Port:       int
		Interface?: string
		#meta: multithreaded: bool | *false
		#meta: direction:     "OUT"
		#meta: type:          "datasource"
		...
	}
	UDPReceiver: {
		Port:     int
		Address?: string
		#meta: multithreaded: bool | *false
		#meta: direction:     "IN"
		#meta: type:          "datasource"
		...
	}
	UDPSender: {
		Destination: string
		#meta: multithreaded: bool | *false
		#meta: direction:     "OUT"
		#meta: type:          "datasource"
		...
	}
	FileReader: {
		Filename:     string
		Format?:      string
		Interpolate?: string
		#meta: multithreaded: bool | *false
		#meta: direction:     "IN"
		#meta: type:          "datasource"
		...
	}
	FileWriter: {
		Filename:        string
		Format?:         string
		StoreOnTrigger?: int
		#meta: multithreaded: bool | *false
		#meta: direction:     "OUT"
		#meta: type:          "datasource"
		...
	}
	OrderedClass: {
		First:  int
		Second: string
		...
	}
	BaseLib2GAM: {
		#meta: type: "gam"
		...
	}
	ConversionGAM: {
		#meta: type: "gam"
		...
	}
	DoubleHandshakeGAM: {
		#meta: type: "gam"
		...
	}
	FilterGAM: {
		Num: [...]
		Den: [...]
		ResetInEachState?: _
		InputSignals?: {...}
		OutputSignals?: {...}
		#meta: type: "gam"
		...
	}
	HistogramGAM: {
		BeginCycleNumber?:     int
		StateChangeResetName?: string
		InputSignals?: {...}
		OutputSignals?: {...}
		#meta: type: "gam"
		...
	}
	Interleaved2FlatGAM: {
		#meta: type: "gam"
		...
	}
	FlattenedStructIOGAM: {
		#meta: type: "gam"
		...
	}
	MathExpressionGAM: {
		Expression: string
		InputSignals?: {...}
		OutputSignals?: {...}
		#meta: type: "gam"
		...
	}
	MessageGAM: {
		#meta: type: "gam"
		...
	}
	MuxGAM: {
		#meta: type: "gam"
		...
	}
	SimulinkWrapperGAM: {
		#meta: type: "gam"
		...
	}
	SSMGAM: {
		#meta: type: "gam"
		...
	}
	StatisticsGAM: {
		#meta: type: "gam"
		...
	}
	TimeCorrectionGAM: {
		#meta: type: "gam"
		...
	}
	TriggeredIOGAM: {

		#meta: type: "gam"
		...
	}
	WaveformGAM: {
		#meta: type: "gam"
		...
	}
	DAN: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "OUT"
		...
	}
	LinuxTimer: {
		ExecutionMode?:   string
		SleepNature?:     string
		SleepPercentage?: _
		Phase?:           int
		CPUMask?:         int
		TimeProvider?: {...}
		Signals: {...}
		#meta: multithreaded: bool | *false
		#meta: direction:     "IN"
		#meta: type:          "datasource"
		...
	}
	LinkDataSource: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "INOUT"
		#meta: type:          "datasource"
		...
	}
	MDSReader: {
		TreeName:   string
		ShotNumber: int
		Frequency:  float | int
		Signals: {...}
		#meta: multithreaded: bool | *false
		#meta: direction:     "IN"
		#meta: type:          "datasource"
		...
	}
	MDSWriter: {
		NumberOfBuffers:       int
		CPUMask:               int
		StackSize:             int
		TreeName:              string
		PulseNumber?:          int
		StoreOnTrigger:        int
		EventName:             string
		TimeRefresh:           float | int
		NumberOfPreTriggers?:  int
		NumberOfPostTriggers?: int
		Signals: {...}
		Messages?: {...}
		#meta: multithreaded: bool | *false
		#meta: direction:     "OUT"
		#meta: type:          "datasource"
		...
	}
	NI1588TimeStamp: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "IN"
		#meta: type:          "datasource"
		...
	}
	NI6259ADC: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "IN"
		#meta: type:          "datasource"
		...
	}
	NI6259DAC: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "OUT"
		#meta: type:          "datasource"
		...
	}
	NI6259DIO: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "INOUT"
		#meta: type:          "datasource"
		...
	}
	NI6368ADC: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "IN"
		#meta: type:          "datasource"
		...
	}
	NI6368DAC: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "OUT"
		#meta: type:          "datasource"
		...
	}
	NI6368DIO: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "INOUT"
		#meta: type:          "datasource"
		...
	}
	NI9157CircularFifoReader: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "IN"
		#meta: type:          "datasource"
		...
	}
	NI9157MxiDataSource: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "INOUT"
		#meta: type:          "datasource"
		...
	}
	OPCUADSInput: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "IN"
		#meta: type:          "datasource"
		...
	}
	OPCUADSOutput: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "OUT"
		#meta: type:          "datasource"
		...
	}
	RealTimeThreadAsyncBridge: {
		#meta: direction:     "INOUT"
		#meta: multithreaded: bool | true
		#meta: type:          "datasource"
		...
	}
	RealTimeThreadSynchronisation: {...}
	UARTDataSource: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "INOUT"
		#meta: type:          "datasource"
		...
	}
	BaseLib2Wrapper: {...}
	EPICSCAClient: {...}
	EPICSPVA: {...}
	MemoryGate: {...}
	OPCUA: {...}
	SysLogger: {...}
	GAMDataSource: {
		#meta: multithreaded: false
		#meta: direction:     "INOUT"
		#meta: type:          "datasource"
		...
	}
}

#Meta: {
	direction?:     "IN" | "OUT" | "INOUT"
	multithreaded?: bool
	...
}

// Definition for any Object.
// It must have a Class field.
// Based on Class, it validates against #Classes.
#Object: {
	Class:    string
	"#meta"?: #Meta
	// Allow any other field by default (extensibility),
	// unless #Classes definition is closed.
	// We allow open structs now.
	...

	// Unify if Class is known.
	// If Class is NOT in #Classes, this might fail or do nothing depending on CUE logic.
	// Actually, `#Classes[Class]` fails if key is missing.
	// This ensures we validate against known classes.
	// If we want to allow unknown classes, we need a check.
	// But spec implies validation should check known classes.
	#Classes[Class]
}
