import { useState, useRef, useEffect } from 'react'
import { formatPortLabel, searchServices, getServiceByPort } from '../data/servicePortRegistry'

interface ServicePortSelectorProps {
  selectedPorts: number[]
  onPortsChange: (ports: number[]) => void
  size?: 'sm' | 'md'
}

function ServicePortSelector({ selectedPorts, onPortsChange, size = 'md' }: ServicePortSelectorProps) {
  const [searchInput, setSearchInput] = useState('')
  const [isDropdownOpen, setIsDropdownOpen] = useState(false)
  const [highlightedIndex, setHighlightedIndex] = useState(0)
  const containerRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)

  const filteredServices = searchServices(searchInput, selectedPorts)

  // Check if the search input is a valid custom port number not in the filtered results
  const parsedPortNumber = parseInt(searchInput.trim(), 10)
  const isValidCustomPort = !isNaN(parsedPortNumber)
    && parsedPortNumber >= 1
    && parsedPortNumber <= 65535
    && !selectedPorts.includes(parsedPortNumber)
  const customPortAlreadyInResults = filteredServices.some(entry => entry.port === parsedPortNumber)
  const showCustomPortOption = isValidCustomPort && !customPortAlreadyInResults

  const totalOptions = filteredServices.length + (showCustomPortOption ? 1 : 0)

  // Close dropdown when clicking outside
  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(event.target as Node)) {
        setIsDropdownOpen(false)
      }
    }
    document.addEventListener('mousedown', handleClickOutside)
    return () => document.removeEventListener('mousedown', handleClickOutside)
  }, [])

  // Reset highlight when results change
  useEffect(() => {
    setHighlightedIndex(0)
  }, [searchInput])

  const addPort = (portNumber: number) => {
    if (!selectedPorts.includes(portNumber)) {
      const updatedPorts = [...selectedPorts, portNumber].sort((portA, portB) => portA - portB)
      onPortsChange(updatedPorts)
    }
    setSearchInput('')
    setIsDropdownOpen(false)
    inputRef.current?.focus()
  }

  const removePort = (portToRemove: number) => {
    const updatedPorts = selectedPorts.filter(portValue => portValue !== portToRemove)
    onPortsChange(updatedPorts)
  }

  const handleKeyDown = (event: React.KeyboardEvent) => {
    if (event.key === 'Enter') {
      event.preventDefault()
      if (totalOptions > 0 && isDropdownOpen) {
        if (highlightedIndex < filteredServices.length) {
          addPort(filteredServices[highlightedIndex].port)
        } else if (showCustomPortOption) {
          addPort(parsedPortNumber)
        }
      } else if (isValidCustomPort) {
        addPort(parsedPortNumber)
      }
    } else if (event.key === 'ArrowDown') {
      event.preventDefault()
      setHighlightedIndex(prevIndex => Math.min(prevIndex + 1, totalOptions - 1))
    } else if (event.key === 'ArrowUp') {
      event.preventDefault()
      setHighlightedIndex(prevIndex => Math.max(prevIndex - 1, 0))
    } else if (event.key === 'Escape') {
      setIsDropdownOpen(false)
    }
  }

  const isSmall = size === 'sm'
  const pillTextClass = isSmall ? 'text-xs' : 'text-sm'
  const pillPaddingClass = isSmall ? 'px-2 py-0.5' : 'px-2 py-0.5'
  const inputClass = isSmall ? 'py-1 text-sm' : ''
  const minHeightClass = isSmall ? 'min-h-[28px]' : 'min-h-[32px]'

  return (
    <div ref={containerRef} className="relative">
      {/* Pills area */}
      <div className={`flex flex-wrap gap-1.5 mb-2 ${minHeightClass}`}>
        {selectedPorts.length === 0 ? (
          <span className="text-dark-500 text-sm italic">No ports configured</span>
        ) : (
          selectedPorts.map(portValue => {
            const serviceEntry = getServiceByPort(portValue)
            return (
              <span
                key={portValue}
                className={`inline-flex items-center gap-1 ${pillPaddingClass} bg-dark-700 rounded ${pillTextClass} text-dark-200 group hover:bg-dark-600`}
              >
                {serviceEntry ? (
                  <>
                    <span className="text-dark-100">{serviceEntry.service}</span>
                    <span className="text-dark-400">({portValue})</span>
                  </>
                ) : (
                  portValue
                )}
                <button
                  onClick={() => removePort(portValue)}
                  className="text-dark-400 hover:text-red-400 transition-colors"
                >
                  <svg className="w-3 h-3" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
                  </svg>
                </button>
              </span>
            )
          })
        )}
      </div>

      {/* Search input */}
      <div className="relative">
        <input
          ref={inputRef}
          type="text"
          value={searchInput}
          onChange={(event) => {
            setSearchInput(event.target.value)
            setIsDropdownOpen(true)
          }}
          onFocus={() => {
            if (searchInput.trim()) setIsDropdownOpen(true)
          }}
          onKeyDown={handleKeyDown}
          placeholder="Search services or enter port..."
          className={`w-full input ${inputClass}`}
        />

        {/* Dropdown */}
        {isDropdownOpen && totalOptions > 0 && (
          <div className="absolute z-50 w-full mt-1 bg-dark-800 border border-dark-600 rounded-lg shadow-xl max-h-[240px] overflow-y-auto">
            {filteredServices.map((entry, optionIndex) => (
              <button
                key={entry.port}
                onClick={() => addPort(entry.port)}
                onMouseEnter={() => setHighlightedIndex(optionIndex)}
                className={`w-full text-left px-3 py-2 text-sm flex items-center justify-between transition-colors ${
                  optionIndex === highlightedIndex
                    ? 'bg-primary-600/20 text-white'
                    : 'text-dark-200 hover:bg-dark-700'
                }`}
              >
                <span>{entry.service}</span>
                <span className="text-dark-400 text-xs">{entry.port}</span>
              </button>
            ))}
            {showCustomPortOption && (
              <button
                onClick={() => addPort(parsedPortNumber)}
                onMouseEnter={() => setHighlightedIndex(filteredServices.length)}
                className={`w-full text-left px-3 py-2 text-sm flex items-center gap-2 border-t border-dark-700 transition-colors ${
                  highlightedIndex === filteredServices.length
                    ? 'bg-primary-600/20 text-white'
                    : 'text-dark-300 hover:bg-dark-700'
                }`}
              >
                <svg className="w-4 h-4 text-dark-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
                </svg>
                Add custom port {parsedPortNumber}
              </button>
            )}
          </div>
        )}
      </div>
    </div>
  )
}

export default ServicePortSelector
