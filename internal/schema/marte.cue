package schema

#Classes: {
	RealTimeApplication: {
		Functions: {...} // type: node
		Data!: {...} // type: node
		States!: {...} // type: node
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
		[_ = !~"^(Class|ENTER)$"]: StateMachineEvent
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
		...
	}
	TimingDataSource: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "IN"
		...
	}
	IOGAM: {
		InputSignals?: {...} // type: node
		OutputSignals?: {...} // type: node
		...
	}
	ReferenceContainer: {
		...
	}
	ConstantGAM: {
		...
	}
	PIDGAM: {
		Kp: float | int // type: float (allow int as it promotes)
		Ki: float | int
		Kd: float | int
		...
	}
	FileDataSource: {
		Filename: string
		Format?:  string
		#meta: multithreaded: bool | *false
		#meta: direction:     "INOUT"
		...
	}
	LoggerDataSource: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "OUT"
		...
	}
	DANStream: {
		Timeout?: int
		#meta: multithreaded: bool | *false
		#meta: direction:     "OUT"
		...
	}
	EPICSCAInput: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "IN"
		...
	}
	EPICSCAOutput: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "OUT"
		...
	}
	EPICSPVAInput: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "IN"
		...
	}
	EPICSPVAOutput: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "OUT"
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
		...
	}
	SDNPublisher: {
		Address:    string
		Port:       int
		Interface?: string
		#meta: multithreaded: bool | *false
		#meta: direction:     "OUT"
		...
	}
	UDPReceiver: {
		Port:     int
		Address?: string
		#meta: multithreaded: bool | *false
		#meta: direction:     "IN"
		...
	}
	UDPSender: {
		Destination: string
		#meta: multithreaded: bool | *false
		#meta: direction:     "OUT"
		...
	}
	FileReader: {
		Filename:     string
		Format?:      string
		Interpolate?: string
		#meta: multithreaded: bool | *false
		#meta: direction:     "IN"
		...
	}
	FileWriter: {
		Filename:        string
		Format?:         string
		StoreOnTrigger?: int
		#meta: multithreaded: bool | *false
		#meta: direction:     "OUT"
		...
	}
	OrderedClass: {
		First:  int
		Second: string
		...
	}
	BaseLib2GAM: {...}
	ConversionGAM: {...}
	DoubleHandshakeGAM: {...}
	FilterGAM: {
		Num: [...]
		Den: [...]
		ResetInEachState?: _
		InputSignals?: {...}
		OutputSignals?: {...}
		...
	}
	HistogramGAM: {
		BeginCycleNumber?:     int
		StateChangeResetName?: string
		InputSignals?: {...}
		OutputSignals?: {...}
		...
	}
	Interleaved2FlatGAM: {...}
	FlattenedStructIOGAM: {...}
	MathExpressionGAM: {
		Expression: string
		InputSignals?: {...}
		OutputSignals?: {...}
		...
	}
	MessageGAM: {...}
	MuxGAM: {...}
	SimulinkWrapperGAM: {...}
	SSMGAM: {...}
	StatisticsGAM: {...}
	TimeCorrectionGAM: {...}
	TriggeredIOGAM: {...}
	WaveformGAM: {...}
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
		...
	}
	LinkDataSource: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "INOUT"
		...
	}
	MDSReader: {
		TreeName:   string
		ShotNumber: int
		Frequency:  float | int
		Signals: {...}
		#meta: multithreaded: bool | *false
		#meta: direction:     "IN"
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
		...
	}
	NI1588TimeStamp: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "IN"
		...
	}
	NI6259ADC: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "IN"
		...
	}
	NI6259DAC: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "OUT"
		...
	}
	NI6259DIO: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "INOUT"
		...
	}
	NI6368ADC: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "IN"
		...
	}
	NI6368DAC: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "OUT"
		...
	}
	NI6368DIO: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "INOUT"
		...
	}
	NI9157CircularFifoReader: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "IN"
		...
	}
	NI9157MxiDataSource: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "INOUT"
		...
	}
	OPCUADSInput: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "IN"
		...
	}
	OPCUADSOutput: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "OUT"
		...
	}
	RealTimeThreadAsyncBridge: {
		#meta: direction:     "INOUT"
		#meta: multithreaded: bool | true
		...
	}
	RealTimeThreadSynchronisation: {...}
	UARTDataSource: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "INOUT"
		...
	}
	BaseLib2Wrapper: {...}
	EPICSCAClient: {...}
	EPICSPVA: {...}
	MemoryGate: {...}
	OPCUA: {...}
	SysLogger: {...}
	GAMDataSource: {
		#meta: multithreaded: bool | *false
		#meta: direction:     "INOUT"
		...
	}
}

// Definition for any Object.
// It must have a Class field.
// Based on Class, it validates against #Classes.
#Object: {
	Class: string
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
