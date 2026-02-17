package jobs

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

//A task running in LocalAI
type Job struct{
	ID	string	`json:"job_id"`
	Type	string `json:"type"`
	Model	string `json:"model"`
	StartTime	time.Time	`json:"start_time"`
	Status	string	`json:"status"`
	ClientIP	string	`json:"client_ip"`
}

//All jobs
type JobStore struct{
	jobs	map[string]*Job
	mu	sync.RWMutex
}

var currentStore *JobStore
var once sync.Once

//Return singleton instance of the JobStore
func GetStore() *JobStore{
	once.Do(func(){
		currentStore=&JobStore{
			jobs: make(map[string]*Job),
		}
	})
	return currentStore
}

//Add new job to the store
func (s *JobStore) AddJob(j *Job){
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[j.ID]=j
}

//Return specific job
func (s *JobStore) GetJob(id string) *Job{
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.jobs[id]
}

//Return a list of all jobs
func (s *JobStore) GetAllJobs() []*Job{
	s.mu.RLock()
	defer s.mu.RUnlock()

	var list []*Job
	for _,job:=range s.jobs{
		list=append(list,job)
	}
	return list
}

//Update status of a job
func (s *JobStore) UpdateStatus(id string,status string){
	s.mu.Lock()
	defer s.mu.Unlock()

	if job,exists:=s.jobs[id]; exists{
		job.Status=status
	}
}

//Helper function to generate a new job
func CreateJob(jobType,model,clientIP string) *Job{
	return &Job{
		ID:	uuid.New().String(),
		Type:	jobType,
		Model:	model,
		StartTime: time.Now(),
		Status: "executing",
		ClientIP: clientIP,
	}
}