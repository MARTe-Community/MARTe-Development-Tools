# Creating a MARTe Application with mdt

This tutorial will guide you through creating, building, and validating a complete MARTe application using the `mdt` toolset.

## Prerequisites

- `mdt` installed and available in your PATH.
- `make` (optional but recommended).

## Step 1: Initialize the Project

Start by creating a new project named `MyControlApp`.

```bash
mdt init MyControlApp
cd MyControlApp
```

This command creates a standard project structure:

- `Makefile`: For building and checking the project.
- `.marte_schema.cue`: For defining custom schemas (if needed).
- `src/app.marte`: The main application definition.
- `src/components.marte`: A placeholder for defining components (DataSources).

## Step 2: Define Components

Open `src/components.marte`. This file uses the `#package App.Data` namespace, meaning all definitions here will be children of `App.Data`.

Let's define a **Timer** (input source) and a **Logger** (output destination).

```marte
#package MyContollApp.App.Data

+DDB = {
    Class = GAMDataSource
}
+TimingDataSource = {
    Class = TimingDataSource
}
+Timer = {
    Class = LinuxTimer
    Signals = {
        Counter = {
            Type = uint32
        }
        Time = {
            Type = uint32
        }
    }
}

+Logger = {
    Class = LoggerDataSource
    Signals = {
        LogValue = {
            Type = float32
        }
    }
}
```

## Step 3: Implement Logic (GAM)

Open `src/app.marte`. This file defines the `App` node.

We will add a GAM that takes the time from the Timer, converts it, and logs it.

Add the GAM definition inside the `+Main` object (or as a separate object if you prefer modularity). Let's modify `src/app.marte`:

```marte
#package MyContollApp
+App = {
    Class = RealTimeApplication
    +Functions = {
        Class = RefenceContainer
        // Define the GAM
        +Converter = {
            Class = IOGAM
            InputSignals = {
                TimeIn = {
                    DataSource = Timer
                    Type = uint32
                    Frequency = 100 //Hz
                    Alias = Time // Refers to 'Time' signal in Timer
                }
            }
            OutputSignals = {
                LogOut = {
                    DataSource = Logger
                    Type = float32
                    Alias = LogValue
                }
            }
        }
    }
    +States = {
        Class = ReferenceContainer
        +Run = {
            Class = RealTimeState
            +MainThread = {
                Class = RealTimeThread
                Functions = { Converter } // Run our GAM
            }
        }
    }

    +Data = {
        Class = ReferenceContainer
        DefaultDataSource = DDB
    }
    +Scheduler = {
        Class = GAMScheduler
        TimingDataSource = TimingDataSource
    }
}
```

## Step 4: Validate

Run the validation check to ensure everything is correct (types match, references are valid).

```bash
mdt check src/*.marte
```

Or using Make:

```bash
make check
```

If you made a mistake (e.g., mismatched types), `mdt` will report an error.

## Step 5: Build

Merge all files into a single configuration file.

```bash
mdt build -o final_app.marte src/*.marte
```

Or using Make:

```bash
make build
```

This produces `app.marte` (or `final_app.marte`), which contains the flattened, merged configuration ready for the MARTe framework.

## Step 6: Advanced - Custom Schema

Suppose you want to enforce that your DataSources support multithreading. You can modify `.marte_schema.cue`.

```cue
package schema

#Classes: {
    // Enforce that LinuxTimer must be multithreaded (example)
    LinuxTimer: {
        #meta: {
            multithreaded: true
        }
        ...
    }
}
```

Now, if you use `LinuxTimer` in multiple threads, `mdt check` will allow it (because of `#meta.multithreaded: true`). By default, it would disallow it.

## Conclusion

You have successfully initialized, implemented, validated, and built a MARTe application using `mdt`.
