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
		direction: "IN"
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
		Filename:  string
		Format?:   string
		direction: "INOUT"
		...
	}
	LoggerDataSource: {
		direction: "OUT"
		...
	}
	DANStream: {
		Timeout?:  int
		direction: "OUT"
		...
	}
	EPICSCAInput: {
		direction: "IN"
		...
	}
	EPICSCAOutput: {
		direction: "OUT"
		...
	}
	EPICSPVAInput: {
		direction: "IN"
		...
	}
	EPICSPVAOutput: {
		direction: "OUT"
		...
	}
	SDNSubscriber: {
		Address:    string
		Port:       int
		Interface?: string
		direction:  "IN"
		...
	}
	SDNPublisher: {
		Address:    string
		Port:       int
		Interface?: string
		direction:  "OUT"
		...
	}
	UDPReceiver: {
		Port:      int
		Address?:  string
		direction: "IN"
		...
	}
	UDPSender: {
		Destination: string
		direction:   "OUT"
		...
	}
	FileReader: {
		Filename:     string
		Format?:      string
		Interpolate?: string
		direction:    "IN"
		...
	}
	FileWriter: {
		Filename:        string
		Format?:         string
		StoreOnTrigger?: int
		direction:       "OUT"
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
		direction: "OUT"
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
		direction: "IN"
		...
	}
	LinkDataSource: {
		direction: "INOUT"
		...
	}
	MDSReader: {
		TreeName:   string
		ShotNumber: int
		Frequency:  float | int
		Signals: {...}
		direction: "IN"
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
		direction: "OUT"
		...
	}
	NI1588TimeStamp: {
		direction: "IN"
		...
	}
	NI6259ADC: {
		direction: "IN"
		...
	}
	NI6259DAC: {
		direction: "OUT"
		...
	}
	NI6259DIO: {
		direction: "INOUT"
		...
	}
	NI6368ADC: {
		direction: "IN"
		...
	}
	NI6368DAC: {
		direction: "OUT"
		...
	}
	NI6368DIO: {
		direction: "INOUT"
		...
	}
	NI9157CircularFifoReader: {
		direction: "IN"
		...
	}
	NI9157MxiDataSource: {
		direction: "INOUT"
		...
	}
	OPCUADSInput: {
		direction: "IN"
		...
	}
	OPCUADSOutput: {
		direction: "OUT"
		...
	}
	RealTimeThreadAsyncBridge: {...}
	RealTimeThreadSynchronisation: {...}
	UARTDataSource: {
		direction: "INOUT"
		...
	}
	BaseLib2Wrapper: {...}
	EPICSCAClient: {...}
	EPICSPVA: {...}
	MemoryGate: {...}
	OPCUA: {...}
	SysLogger: {...}
	GAMDataSource: {
		direction: "INOUT"
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
