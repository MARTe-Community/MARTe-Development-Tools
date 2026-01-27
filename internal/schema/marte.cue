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
		#multithreaded: bool | *false
		#direction:     "IN"
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
		Filename:       string
		Format?:        string
		#multithreaded: bool | *false
		#direction:     "INOUT"
		...
	}
	LoggerDataSource: {
		#multithreaded: bool | *false
		#direction:     "OUT"
		...
	}
	DANStream: {
		Timeout?:       int
		#multithreaded: bool | *false
		#direction:     "OUT"
		...
	}
	EPICSCAInput: {
		#multithreaded: bool | *false
		#direction:     "IN"
		...
	}
	EPICSCAOutput: {
		#multithreaded: bool | *false
		#direction:     "OUT"
		...
	}
	EPICSPVAInput: {
		#multithreaded: bool | *false
		#direction:     "IN"
		...
	}
	EPICSPVAOutput: {
		#multithreaded: bool | *false
		#direction:     "OUT"
		...
	}
	SDNSubscriber: {
		Address:        string
		Port:           int
		Interface?:     string
		#multithreaded: bool | *false
		#direction:     "IN"
		...
	}
	SDNPublisher: {
		Address:        string
		Port:           int
		Interface?:     string
		#multithreaded: bool | *false
		#direction:     "OUT"
		...
	}
	UDPReceiver: {
		Port:           int
		Address?:       string
		#multithreaded: bool | *false
		#direction:     "IN"
		...
	}
	UDPSender: {
		Destination:    string
		#multithreaded: bool | *false
		#direction:     "OUT"
		...
	}
	FileReader: {
		Filename:       string
		Format?:        string
		Interpolate?:   string
		#multithreaded: bool | *false
		#direction:     "IN"
		...
	}
	FileWriter: {
		Filename:        string
		Format?:         string
		StoreOnTrigger?: int
		#multithreaded:  bool | *false
		#direction:      "OUT"
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
		#multithreaded: bool | *false
		#direction:     "OUT"
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
		#multithreaded: bool | *false
		#direction:     "IN"
		...
	}
	LinkDataSource: {
		#multithreaded: bool | *false
		#direction:     "INOUT"
		...
	}
	MDSReader: {
		TreeName:   string
		ShotNumber: int
		Frequency:  float | int
		Signals: {...}
		#multithreaded: bool | *false
		#direction:     "IN"
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
		#multithreaded: bool | *false
		#direction:     "OUT"
		...
	}
	NI1588TimeStamp: {
		#multithreaded: bool | *false
		#direction:     "IN"
		...
	}
	NI6259ADC: {
		#multithreaded: bool | *false
		#direction:     "IN"
		...
	}
	NI6259DAC: {
		#multithreaded: bool | *false
		#direction:     "OUT"
		...
	}
	NI6259DIO: {
		#multithreaded: bool | *false
		#direction:     "INOUT"
		...
	}
	NI6368ADC: {
		#multithreaded: bool | *false
		#direction:     "IN"
		...
	}
	NI6368DAC: {
		#multithreaded: bool | *false
		#direction:     "OUT"
		...
	}
	NI6368DIO: {
		#multithreaded: bool | *false
		#direction:     "INOUT"
		...
	}
	NI9157CircularFifoReader: {
		#multithreaded: bool | *false
		#direction:     "IN"
		...
	}
	NI9157MxiDataSource: {
		#multithreaded: bool | *false
		#direction:     "INOUT"
		...
	}
	OPCUADSInput: {
		#multithreaded: bool | *false
		#direction:     "IN"
		...
	}
	OPCUADSOutput: {
		#multithreaded: bool | *false
		#direction:     "OUT"
		...
	}
	RealTimeThreadAsyncBridge: {
		#direction:     "INOUT"
		#multithreaded: bool | true
		...
	}
	RealTimeThreadSynchronisation: {...}
	UARTDataSource: {
		#multithreaded: bool | *false
		#direction:     "INOUT"
		...
	}
	BaseLib2Wrapper: {...}
	EPICSCAClient: {...}
	EPICSPVA: {...}
	MemoryGate: {...}
	OPCUA: {...}
	SysLogger: {...}
	GAMDataSource: {
		#multithreaded: bool | *false
		#direction:     "INOUT"
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
