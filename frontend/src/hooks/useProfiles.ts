import { useState, useEffect, useCallback } from 'react'
import { ProfileStatus, Profile } from '../App'

export function useProfiles() {
  const [profiles, setProfiles] = useState<ProfileStatus[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const fetchProfiles = useCallback(async () => {
    try {
      const data = await window.go.app.App.GetProfiles()
      setProfiles(data || [])
      setError(null)
    } catch (err) {
      setError(String(err))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchProfiles()
    const interval = setInterval(fetchProfiles, 2000)
    return () => clearInterval(interval)
  }, [fetchProfiles])

  const connect = async (id: string) => {
    try {
      await window.go.app.App.Connect(id)
      await fetchProfiles()
    } catch (err) {
      throw err
    }
  }

  const disconnect = async (id: string) => {
    try {
      await window.go.app.App.Disconnect(id)
      await fetchProfiles()
    } catch (err) {
      throw err
    }
  }

  const deleteProfile = async (id: string, deleteConfigFile: boolean = false) => {
    try {
      await window.go.app.App.DeleteProfile(id, deleteConfigFile)
      await fetchProfiles()
    } catch (err) {
      throw err
    }
  }

  const getProfile = async (id: string): Promise<Profile | null> => {
    try {
      return await window.go.app.App.GetProfile(id)
    } catch (err) {
      throw err
    }
  }

  return {
    profiles,
    loading,
    error,
    connect,
    disconnect,
    deleteProfile,
    getProfile,
    refresh: fetchProfiles,
  }
}
