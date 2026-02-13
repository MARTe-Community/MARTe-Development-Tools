package schema

import "list"

#Classes: {
	RealTimeApplication: {
		Functions!: {
			Class: "ReferenceContainer"
			[_= !~"^Class$"]: {
				#meta: MetaType: "gam"
				...
			}
		} // type: node
		Data!: {
			Class:             "ReferenceContainer"
			DefaultDataSource: string
			[_= !~"^(Class|DefaultDataSource)$"]: {
				#meta: MetaType: "datasource"
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
			#meta: MetaType: "scheduler"
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
		#meta: MetaType: "scheduler"
		...
	}
	TimingDataSource: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "IN"
		#meta: MetaType:          "datasource"
		...
	}
	IOGAM: {
		InputSignals: { [_]: { ByteSize: int, ... } }
		OutputSignals: { [_]: { ByteSize: int, ... } }
		#meta: MetaType: "gam"
		
		InputSize: list.Sum([for k, v in InputSignals { v.ByteSize }])
		OutputSize: list.Sum([for k, v in OutputSignals { v.ByteSize }])
		
		InputSize: OutputSize
	}
	ReferenceContainer: {
		...
	}
	ConstantGAM: {
		...
		#meta: MetaType: "gam"
	}
	PIDGAM: {
		Kp: float | int // type: float (allow int as it promotes)
		Ki: float | int
		Kd: float | int
		SamplingTime?: float | int
		InputSignals: {...}
		OutputSignals: {...}
		#meta: MetaType: "gam"
		...
	}
	FileDataSource: {
		Filename: string
		Format?:  string
		#meta: multithreaded: bool | *false
		#meta: direction:     "INOUT"
		#meta: MetaType:          "datasource"
		...
	}
	LoggerDataSource: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "OUT"
		#meta: MetaType:          "datasource"
		...
	}
	DANStream: {
		Timeout?: int
		#meta: multithreaded: bool | *false
		#meta: direction:     "OUT"
		#meta: MetaType:          "datasource"
		...
	}
	EPICSCAInput: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "IN"
		#meta: MetaType:          "datasource"
		...
	}
	EPICSCAOutput: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "OUT"
		#meta: MetaType:          "datasource"
		...
	}
	EPICSPVAInput: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "IN"
		#meta: MetaType:          "datasource"
		...
	}
	EPICSPVAOutput: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "OUT"
		#meta: MetaType:          "datasource"
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
		#meta: MetaType:          "datasource"
		...
	}
	SDNPublisher: {
		Address:    string
		Port:       int
		Interface?: string
		Topic?:     string
		#meta: multithreaded: bool | *false
		#meta: direction:     "OUT"
		#meta: MetaType:          "datasource"
		...
	}
	UDPReceiver: {
		Port:     int
		Address?: string
		#meta: multithreaded: bool | *false
		#meta: direction:     "IN"
		#meta: MetaType:          "datasource"
		...
	}
	UDPSender: {
		Destination: string
		#meta: multithreaded: bool | *false
		#meta: direction:     "OUT"
		#meta: MetaType:          "datasource"
		...
	}
	FileReader: {
		Filename:     string
		Format?:      string
		Interpolate?: string
		#meta: multithreaded: bool | *false
		#meta: direction:     "IN"
		#meta: MetaType:          "datasource"
		...
	}
	FileWriter: {
		Filename:        string
		Format?:         string
		StoreOnTrigger?: int
		#meta: multithreaded: bool | *false
		#meta: direction:     "OUT"
		#meta: MetaType:          "datasource"
		...
	}
	OrderedClass: {
		First:  int
		Second: string
		...
	}
	BaseLib2GAM: {
		#meta: MetaType: "gam"
		...
	}
	ConversionGAM: {
		InputSignals: {...}
		OutputSignals: {...}
		#meta: MetaType: "gam"
		...
	}
	DoubleHandshakeGAM: {
		#meta: MetaType: "gam"
		...
	}
	FilterGAM: {
		Num: [...]
		Den: [...]
		ResetInEachState?: _
		InputSignals: {...}
		OutputSignals: {...}
		#meta: MetaType: "gam"
		...
	}
	HistogramGAM: {
		BeginCycleNumber?:     int
		StateChangeResetName?: string
		InputSignals?: {...}
		OutputSignals?: {...}
		#meta: MetaType: "gam"
		...
	}
	Interleaved2FlatGAM: {
		#meta: MetaType: "gam"
		...
	}
	FlattenedStructIOGAM: {
		#meta: MetaType: "gam"
		...
	}
	MathExpressionGAM: {
		Expression: string
		InputSignals: {...}
		OutputSignals: {...}
		#meta: MetaType: "gam"
		...
	}
	MessageGAM: {
		#meta: MetaType: "gam"
		...
	}
	MuxGAM: {
		InputSignals: {...}
		OutputSignals: {...}
		#meta: MetaType: "gam"
		...
	}
	SimulinkWrapperGAM: {
		Library: string
		Symbol?: string
		InputSignals: {...}
		OutputSignals: {...}
		#meta: MetaType: "gam"
		...
	}
	SSMGAM: {
		#meta: MetaType: "gam"
		...
	}
	StatisticsGAM: {
		InputSignals: {...}
		OutputSignals: {...}
		#meta: MetaType: "gam"
		...
	}
	TimeCorrectionGAM: {
		#meta: MetaType: "gam"
		...
	}
	TriggeredIOGAM: {
		InputSignals: {...}
		OutputSignals: {...}
		#meta: MetaType: "gam"
		...
	}
	WaveformGAM: {
		InputSignals: {...}
		OutputSignals: {...}
		Triggers?: {...}
		#meta: MetaType: "gam"
		...
	}
	DAN: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "OUT"
		...
	}
	LinuxTimer: {
		ExecutionMode?:   string
		SleepNature?:     "Default" | "Busy"
		SleepPercentage?: _
		Phase?:           int
		CPUMask?:         int
		TimeProvider?: {...}
		Signals: {...}
		#meta: multithreaded: bool | *false
		#meta: direction:     "IN"
		#meta: MetaType:          "datasource"
		...
	}
	LinkDataSource: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "INOUT"
		#meta: MetaType:          "datasource"
		...
	}
	MDSReader: {
		TreeName:   string
		ShotNumber: int
		Frequency?:  float | int
		Signals: {...}
		#meta: multithreaded: bool | *false
		#meta: direction:     "IN"
		#meta: MetaType:          "datasource"
		...
	}
	MDSWriter: {
		NumberOfBuffers:       int
		CPUMask:               int
		StackSize:             int
		TreeName:              string
		PulseNumber?:          int
		StoreOnTrigger:        0 | 1
		EventName?:             string
		TimeRefresh?:           float | int
		NumberOfPreTriggers?:  int
		NumberOfPostTriggers?: int
		Signals: {...}
		Messages?: {...}
		#meta: multithreaded: bool | *false
		#meta: direction:     "OUT"
		#meta: MetaType:          "datasource"
		...
	}
	NI1588TimeStamp: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "IN"
		#meta: MetaType:          "datasource"
		...
	}
	NI6259ADC: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "IN"
		#meta: MetaType:          "datasource"
		...
	}
	NI6259DAC: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "OUT"
		#meta: MetaType:          "datasource"
		...
	}
	NI6259DIO: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "INOUT"
		#meta: MetaType:          "datasource"
		...
	}
	NI6368ADC: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "IN"
		#meta: MetaType:          "datasource"
		...
	}
	NI6368DAC: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "OUT"
		#meta: MetaType:          "datasource"
		...
	}
	NI6368DIO: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "INOUT"
		#meta: MetaType:          "datasource"
		...
	}
	NI9157CircularFifoReader: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "IN"
		#meta: MetaType:          "datasource"
		...
	}
	NI9157MxiDataSource: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "INOUT"
		#meta: MetaType:          "datasource"
		...
	}
	OPCUADSInput: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "IN"
		#meta: MetaType:          "datasource"
		...
	}
	OPCUADSOutput: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "OUT"
		#meta: MetaType:          "datasource"
		...
	}
	RealTimeThreadAsyncBridge: {
		#meta: direction:     "INOUT"
		#meta: multithreaded: bool | true
		#meta: MetaType:          "datasource"
		...
	}
	RealTimeThreadSynchronisation: {...}
	UARTDataSource: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "INOUT"
		#meta: MetaType:          "datasource"
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
		#meta: MetaType:          "datasource"
		...
	}
}

#Meta: {
	direction?:     "IN" | "OUT" | "INOUT"
	multithreaded?: bool
	MetaType?:      string
	type?:          string // Keep for backward compatibility
	Parent?: {
		Name?:     string
		Class?:    string
		MetaType?: string
	}
}

#Object: {
	Class:    string
	"#meta"?: #Meta
	...
	#Classes[Class]
}