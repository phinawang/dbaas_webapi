package main

import (
    "time"
    _"io"
    _"io/ioutil"
    "os"
    "log"
    _"net/http"
    "github.com/gin-gonic/gin"
    "fmt"
    "os/exec"
    "github.com/bitly/go-simplejson"
    "gopkg.in/mgo.v2"
    "gopkg.in/mgo.v2/bson"
    "strings"
    "database/sql"
    _ "github.com/go-sql-driver/mysql"
    _"github.com/BurntSushi/toml"
    _"encoding/json"
    _"bytes"
)

func v1_postProvistionDatabase(c *gin.Context, l *log.Logger, logFile string, cg CONFIG) {
    apiName    := "postProvistionDatabase"
    token      := c.Request.Header.Get("Authorization")
    returnData := make(map[string]interface{})
    var forwardResponse []byte
    var statusCode int
    dataCenter := c.Query("datacenter")
    body := getPostBody(c)

    if dataCenter != "" {

        Log(l, logFile, apiName, "forward-datacenter:"+dataCenter)
        forwardHost := getForwardHost(dataCenter)
        forWardUrl := "http://"+forwardHost+"/v1/ansible/databases"
        Log(l, logFile, apiName, "forWardUrl:"+forWardUrl)

        forwardResponse, statusCode = httpRequest(token, "POST", forWardUrl, body, apiName, l, logFile)

        c.Data(statusCode, "application/json; charset=utf-8", forwardResponse)
        
    }else{
   
        if token == os.Getenv("TOKEN") {

            //log.Print("AUTH SUCCESS")
            Log(l, logFile, apiName,"[SUCCESS] AUTH SUCCESS")
            Log(l, logFile, apiName ,"[DEBUG] MONGODB HOST: "+os.Getenv("MONGODB_HOST"))

            //parse JSON
            js, err := simplejson.NewJson([]byte(body))
            
            if err == nil {
                taskId        := js.Get("task_id").MustString()
                clusterId     := js.Get("cluster_id").MustString()
                functionType  := js.Get("func").MustString()
                monitorHosts  := js.Get("monitor_hosts").MustArray()
                dataHosts     := js.Get("data_hosts").MustArray()
                nowTime       := time.Now().UTC().Format(time.RFC3339)
                dbname        := js.Get("dbname").MustString()
                password      := js.Get("password").MustString()
                sshPassword   := js.Get("ssh_password").MustString()
                serviceName   := js.Get("service_name").MustString()
                tenant        := js.Get("tenant").MustString()

                _ = dbname
                _ = password
                _ = sshPassword
                returnData["task_id"] = taskId

     
                //DB connection
                mongoDBDialInfo := &mgo.DialInfo{
                    Addrs:    []string{os.Getenv("MONGODB_HOST")},
                    Timeout:  0 * time.Second,
                    Database: cg.Mongo.AuthDatabase,
                    Username: cg.Mongo.AuthUserName,
                    Password: cg.Mongo.AuthPassword,
                }

                // Create a session which maintains a pool of socket connections
                session, err := mgo.DialWithInfo(mongoDBDialInfo)
                if err == nil {

                    defer session.Close()
                    session.SetMode(mgo.Monotonic, true)
                    mongoConnection := session.DB("ansible").C("provision_task_record")
             
                    var count int
                    
                    //check taskid not exist
                    count, err = mongoConnection.Find(bson.M{"task_id": taskId }).Count()
                    
                    if err != nil {
                        Log(l, logFile, apiName,"[ERROR] MONGO QUERY ERROR")
                        returnData["status"] = "FAILED"
                        returnData["errorcode"] = "BKP00001"
                        returnData["message"] = "mongo query task_id error"
                   
                    }else if(count == 0){   

                        //save payload (cluster_id, func, host_fqdn, host_ip)
                        type Task struct {
                            Task_Id        string         "bson:`task_id`"
                            Cluster_id     string         "bson:`cluster_id`"
                            Function_type  string         "bson:`func`"
                            Monitor_hosts  []interface {} "bson:`monitor_hosts`"
                            Data_hosts     []interface {} "bson:`data_hosts`"
                            Datetime       string         "bson:`datetime`"
                            Tenant         string         "bson:`tenant`"
                            Service_name   string         "bson:`service_name`"
                        }
                        
                        //instert task
                        err = mongoConnection.Insert(&Task{
                            Task_Id:        taskId,
                            Cluster_id:     clusterId,
                            Function_type:  functionType,
                            Monitor_hosts:  monitorHosts,
                            Data_hosts:     dataHosts,
                            Datetime:       nowTime,
                            Tenant:         tenant,
                            Service_name:   serviceName,
                        })

                        if err == nil {
                            
                            //call python command
                            switch functionType {
                                case "mysql_standAlone":

                                    dataNode, ok := dataHosts[0].(map[string]interface{})
                                    
                                    if(!ok){
                                        returnData["status"] = "FAILED"
                                        returnData["errorcode"] = "PDE00005"
                                        returnData["message"] = "Invalide data_hosts"
                                        c.JSON(401, returnData)
                                    }
                                   
                                    fqdn = dataNode["fqdn"].(string)
                                    ip   = dataNode["ip"].(string)
                                    
                                    
                                    args := []string{"mysql", "--taskid", taskId, "--hostname", fqdn, "--ip", ip, "--cluster_id", clusterId}


                                    //optional fields
                                    if dbname != "" {
                                        args = append(args, "--database", dbname)
                                    }

                                    if password != "" {
                                        args = append(args, "--password", password)
                                    }

                                    if serviceName != "" {
                                        args = append(args, "--service_name", serviceName)
                                    }

                                    if tenant != "" {
                                        args = append(args, "--tenant", tenant)
                                    }

                                    justString := strings.Join(args," ")
                                    Log(l, logFile, apiName, "[DEBUG]"+justString)

                                    //execute Ansible
                                    cmd := exec.Command("/usr/bin/pyansibleinv", args...)

                                    cmd.Stdout = os.Stdout
                                    err := cmd.Start()
                                    
                                    if err != nil {
                                        Log(l, logFile, apiName,"[ERROR] RUN ANSIBLE COMMAND ERROR")
                                        returnData["status"] = "FAILED"
                                        returnData["errorcode"] = "PDE00005"
                                        returnData["reson"] = "Run ansible playbook error"
                                    }else{
                                        
                                        //save host(cluster_id, func, host_fqdn, host_ip)
                                        session, err := mgo.DialWithInfo(mongoDBDialInfo)
                                        if err == nil {

                                            defer session.Close()
                                            session.SetMode(mgo.Monotonic, true)
                                            mongoConnection := session.DB("ansible").C("host")

                                            type Task struct {
                                                Cluster_id     string         "bson:`cluster_id`"
                                                Function_type  string         "bson:`func`"
                                                Monitor_hosts  []interface {} "bson:`monitor_hosts`"
                                                Data_hosts     []interface {} "bson:`data_hosts`"
                                                Datetime       string         "bson:`datetime`"
                                                Service_Name   string         "bson:`service_name`"
                                            }
                                            
                                            //instert task
                                            err = mongoConnection.Insert(&Task{
                                                Cluster_id:     clusterId,
                                                Function_type:  functionType,
                                                Monitor_hosts:  monitorHosts,
                                                Data_hosts:     dataHosts,
                                                Datetime:       nowTime,
                                                Service_Name:   serviceName,
                                            })
                                     
                                            if err == nil {
                                                go read_output(cmd)
                                                Log(l, logFile, apiName,"RUN COMMAND SUCCESS")
                                                returnData["status"] = "SUCCESSFUL"

                                            }else {
                                                Log(l, logFile, apiName,"[ERROR] MONGO insert ERROR")
                                                fmt.Println(err)
                                                returnData["status"] = "FAILED"
                                                returnData["errorcode"] = "PDE00003"
                                                returnData["message"] = "Mongodb insert error"
                                            }
                                        } else {
                                            Log(l, logFile, apiName,"[ERROR] MONGODB CONNECTION ERROR")
                                            fmt.Println(err)
                                            returnData["status"] = "FAILED"
                                            returnData["errorcode"] = "PDE00006"
                                            returnData["message"] = "Mongodb connection error"
                                        }

                                    }

                                    break

                                case "mysql_mha":
                                    
                                    monitorHostsString := hostsToString(monitorHosts)
                                    dataHostsString    := hostsToString(dataHosts)
                                    //execute Ansible
                                    //docker exec ansible /usr/bin/pyansibleinv mha --password 'mypass' --sshpass 'mypassword' --taskid 12345 --cluster_id test --data_host mysql01.iad1:10.40.136.13,mysql02.iad1:10.40.136.14 --monitor_host mha01.iad1:10.40.136.11 --db_vip 10.40.136.15                    
                                    args := []string{"mha", "--taskid", taskId, "data_host", dataHostsString, "monitor_host", monitorHostsString, }

                                    //optional fields
                                    if dbname != "" {
                                        args = append(args, "--database", dbname)
                                    }

                                    if password != "" {
                                        args = append(args, "--password", password)
                                    }

                                    if clusterId != ""{
                                        args = append(args, "--cluster_id", clusterId)
                                    }

                                    if serviceName != "" {
                                        args = append(args, "--service_name", serviceName)
                                    }

                                    if tenant != "" {
                                        args = append(args, "--tenant", tenant)
                                    }

                                    cmd := exec.Command("/usr/bin/pyansibleinv", args...)
                                    cmd.Stdout = os.Stdout
                                    err := cmd.Start()
                                    
                                    if err == nil {
                                        
                                        go read_output(cmd)
                                        Log(l, logFile, apiName,"RUN COMMAND SUCCESS")
                                        returnData["status"] = "SUCCESSFUL"

                                    } else {
                                        Log(l, logFile, apiName,"[ERROR] RUN ANSIBLE COMMAND ERROR")
                                        fmt.Println(err)
                                        returnData["status"] = "FAILED"
                                        returnData["errorcode"] = "PDE00005"
                                        returnData["message"] = "Run ansible playbook error"
                                    }
                                    break
                                case "mssql_standAlone":

                                    dataNode, ok := dataHosts[0].(map[string]interface{})
                                    
                                    if(!ok){
                                        returnData["status"] = "FAILED"
                                        returnData["errorcode"] = "PDE00005"
                                        returnData["message"] = "Invalide data_hosts"
                                        c.JSON(401, returnData)
                                    }
                                   
                                    fqdn = dataNode["fqdn"].(string)
                                    ip   = dataNode["ip"].(string)
                                    
                                    ///usr/bin/pyansibleinv mssql --database testd --password mypass --taskid 123456789 --hostname fqdn.sjc1 --ip 192.168.1.1
                                    args := []string{"mssql", "--taskid", taskId, "--hostname", fqdn, "--ip", ip}

                                    //optional fields
                                    if dbname != "" {
                                        args = append(args, "--database", dbname)
                                    }

                                    if password != "" {
                                        args = append(args, "--password", password)
                                    }

                                    /*

                                    if serviceName != "" {
                                        args = append(args, "--service_name", serviceName)
                                    }

                                    if tenant != "" {
                                        args = append(args, "--tenant", tenant)
                                    }
                                    */
                                    //execute Ansible
                                    cmd := exec.Command("/usr/bin/pyansibleinv", args...)
                                    justString := strings.Join(args," ")
                                    Log(l, logFile, apiName, "[DEBUG] RUN COMMAND: pyansibleinv "+justString)



                                    cmd.Stdout = os.Stdout
                                    err := cmd.Start()
                                    
                                    if err != nil {
                                        Log(l, logFile, apiName,"[ERROR] RUN ANSIBLE COMMAND ERROR")
                                        fmt.Println(err)
                                        returnData["status"] = "FAILED"
                                        returnData["errorcode"] = "PDE00005"
                                        returnData["reson"] = "Run ansible playbook error"
                                    }else{
                                        
                                        //save host(cluster_id, func, host_fqdn, host_ip)
                                        session, err := mgo.DialWithInfo(mongoDBDialInfo)
                                        if err == nil {

                                            defer session.Close()
                                            session.SetMode(mgo.Monotonic, true)
                                            mongoConnection := session.DB("ansible").C("host")

                                            type Task struct {
                                                Cluster_id     string         "bson:`cluster_id`"
                                                Function_type  string         "bson:`func`"
                                                Monitor_hosts  []interface {} "bson:`monitor_hosts`"
                                                Data_hosts     []interface {} "bson:`data_hosts`"
                                                Datetime       string         "bson:`datetime`"
                                                Tenant         string         "bson:`tenant`"
                                                Service_name   string         "bson:`service_name`"
                                            }
                                            
                                            //instert task
                                            err = mongoConnection.Insert(&Task{
                                                Cluster_id:     clusterId,
                                                Function_type:  functionType,
                                                Monitor_hosts:  monitorHosts,
                                                Data_hosts:     dataHosts,
                                                Datetime:       nowTime,
                                                Tenant:         tenant,
                                                Service_name:   serviceName,
                                            })
                                     
                                            if err == nil {
                                                go read_output(cmd)
                                                Log(l, logFile, apiName,"RUN COMMAND SUCCESS")
                                                returnData["status"] = "SUCCESSFUL"

                                            }else {
                                                Log(l, logFile, apiName,"[ERROR] MONGO insert ERROR")
                                                fmt.Println(err)
                                                returnData["status"] = "FAILED"
                                                returnData["errorcode"] = "PDE00003"
                                                returnData["message"] = "Mongodb insert error"
                                            }
                                        } else {
                                            Log(l, logFile, apiName,"[ERROR] MONGODB CONNECTION ERROR")
                                            fmt.Println(err)
                                            returnData["status"] = "FAILED"
                                            returnData["errorcode"] = "PDE00006"
                                            returnData["message"] = "Mongodb connection error"
                                        }
                                    }
                                    break

                                default:
                                    Log(l, logFile, apiName,"[ERROR] NOT DEFINE TYPE")
                                    returnData["status"] = "FAILED"
                                    returnData["errorcode"] = "PDE00004"
                                    returnData["message"] = "Using not define function type"
                                    break
                                }
                        }else {
                            Log(l, logFile, apiName,"[ERROR] MONGO insert ERROR")
                            fmt.Println(err)
                            returnData["status"] = "FAILED"
                            returnData["errorcode"] = "PDE00003"
                            returnData["message"] = "Mongodb insert error"
                        }
                    }else{

                        Log(l, logFile, apiName,"[ERROR] BACKUP TASK_ID ALREADY EXIST")
                        returnData["status"] = "FAILED"
                        returnData["errorcode"] = "PDE00007"
                        returnData["message"] = "Task_id already exist"
                    }

                }else{
                    Log(l, logFile, apiName,"[ERROR] MONGODB CONNECTION ERROR")
                    fmt.Println(err)
                    returnData["status"] = "FAILED"
                    returnData["errorcode"] = "PDE00006"
                    returnData["message"] = "Mongodb connection error"
                }

            }else{
                Log(l, logFile, apiName,"[ERROR] JSON FORMAT ERROR")
                fmt.Println(err)
                returnData["status"] = "FAILED"
                returnData["errorcode"] = "GLB00002"
                returnData["message"] = "JSON format error"
            } 

        } else  {
            Log(l, logFile, apiName,"[ERROR] TOKEN AUTH FAILED")
            returnData["status"] = "FAILED"
            returnData["errorcode"] = "GLB00001"
            returnData["message"] = "Auth faild"
        }

        c.JSON(200, returnData)
    }
}

func v1_getProvisionDatabaseTask(c *gin.Context, l *log.Logger, logFile string, cg CONFIG) {
    apiName    := "GetProvisionDatabaseTask"
    token      := c.Request.Header.Get("Authorization")
    returnData := make(map[string]interface{})
    taskId     := c.Param("task_id")
    clusterId  := c.Param("cluster_id")
    var forwardResponse []byte
    var statusCode int
    dataCenter := c.Query("datacenter")

    if dataCenter != "" {
        
        forwardHost := getForwardHost(dataCenter)
        forWardUrl := "http://"+forwardHost+"/v1/ansible/databases/"+clusterId+"/task/"+taskId
        Log(l, logFile, apiName, "forWardUrl:"+forWardUrl)
        forwardResponse, statusCode = httpRequest(token, "GET", forWardUrl, "", apiName, l, logFile)
        fmt.Println("888888")
        c.Data(statusCode, "application/json; charset=utf-8", forwardResponse)
    }else{
    
        if token == os.Getenv("TOKEN") {

            var db *sql.DB
            Log(l, logFile, apiName,"[SUCCESS] AUTH SUCCESS")
            Log(l, logFile, apiName,"[DEBUG] MYSQL HOST: "+os.Getenv("MYSQL_HOST"))
            Log(l, logFile, apiName,"[DEBUG] Account: "+cg.Mysql.Account)
            Log(l, logFile, apiName,"[DEBUG] password: "+cg.Mysql.Password)


            if taskId != ""{
                var err error
                db, err = sql.Open("mysql", cg.Mysql.Account+":"+cg.Mysql.Password+"@("+os.Getenv("MYSQL_HOST")+":"+cg.Mysql.Port+")/"+cg.Mysql.DBName)
                if err == nil {
                    defer db.Close()

                    var (
                        id string
                        failed int
                        ok int
                        playbook_id string
                        complete int
                    )

                    err = db.QueryRow("SELECT id FROM playbooks WHERE id = ? ", taskId).Scan(&id)
                    

                    if err == nil {
                        err = db.QueryRow("select sum(s.failed)+sum(s.unreachable) as failed, sum(s.ok)+sum(s.skipped)+sum(s.changed) as ok, s.playbook_id, p.complete from playbooks p, stats s where p.id=s.playbook_id and  playbook_id = ? group by s.playbook_id, p.complete",taskId).Scan(&failed, &ok, &playbook_id, &complete)
                        
                        if err == nil {

                            if playbook_id != "" {
                                if failed == 0 {
                                    if complete == 1 {
                                        returnData["status"] = "SUCCESSFUL"
                                    } else {
                                        returnData["status"] = "IN PROGRESS"
                                    }
                                }else{
                                    Log(l, logFile, apiName,"[ERROR] AN ERROR OCCURS")
                                    returnData["status"] = "FAILED"
                                    returnData["errorcode"] = "GTK00004"
                                    returnData["message"] = "an error occurs"
                                }
                            } else {
                                Log(l, logFile, apiName,"[ERROR] CANNOT FIND THIS TASK_ID")
                                returnData["status"] = "FAILED"
                                returnData["errorcode"] = "GTK00007"
                                returnData["message"] = "Cannot find this task_id"
                            }
                        } else {
                            if err == sql.ErrNoRows {                        
                                Log(l, logFile, apiName,"IN PROGRESS")
                                returnData["status"] = "IN PROGRESS"
                            } else {
                                Log(l, logFile, apiName,"[ERROR] ANSIBLE TASK QUERY ERROR:"+err.Error())
                                returnData["status"] = "Ansible task query error"
                                returnData["errorcode"] = "GTK00003"
                                returnData["message"] = "Ansible task query error"
                            }
                        }
                    } else {
                        if err == sql.ErrNoRows {
                            Log(l, logFile, apiName,"[ERROR] CANNOT FIND THIS TASK_ID")
                            returnData["status"] = "FAILED"
                            returnData["errorcode"] = "GTK00004"
                            returnData["message"] = "Cannot find this task_id"
                        } else {
                            Log(l, logFile, apiName,"[ERROR] AN ERROR OCCURS ON ANSIBLE SQL QUERY: "+err.Error())
                            returnData["status"] = "FAILED"
                            returnData["errorcode"] = "GTK00005"
                            returnData["message"] = "an error occurs on ansible sql query"
                        }
                    }

                } else {
                    Log(l, logFile, apiName,"[ERROR] MYSQL CONNECTION ERROR: "+err.Error())
                    returnData["status"] = "FAILED"
                    returnData["errorcode"] = "GTK00002"
                    returnData["message"] = "ansible playbook connection error"
                }
 
            }else{
                Log(l, logFile, apiName,"10")
                Log(l, logFile, apiName,"[ERROR] NO TASK_ID IN URL")
                returnData["status"] = "FAILED"
                returnData["errorcode"] = "GTK00001"
                returnData["message"] = "No task_id in url"
            }

        } else  {
            Log(l, logFile, apiName,"[ERROR] TOKEN AUTH FAILED")
            returnData["status"] = "FAILED"
            returnData["errorcode"] = "GLB00001"
            returnData["message"] = "Auth faild"
        }

        c.JSON(200, returnData)
    }
}

func v1_postDatabaseBackup(c *gin.Context, l *log.Logger, logFile string, cg CONFIG) {
    apiName    := "PostDatabaseBackup"
    token      := c.Request.Header.Get("Authorization")
    returnData := make(map[string]interface{})
    
    clusterId  := c.Param("cluster_id")
    var forwardResponse []byte
    var statusCode int
    dataCenter := c.Query("datacenter")

    body := getPostBody(c)

    if dataCenter != "" {

        forwardHost := getForwardHost(dataCenter)
        forWardUrl := "http://"+forwardHost+"/v1/ansible/databases/"+clusterId+"/backup"
        forwardResponse, statusCode = httpRequest(token, "POST", forWardUrl, body, apiName, l, logFile)

        c.Data(statusCode, "application/json; charset=utf-8", forwardResponse)
        
    }else{
   
        if token == os.Getenv("TOKEN") {
            //log.Print("AUTH SUCCESS")
            Log(l, logFile, apiName,"[SUCCESS] AUTH SUCCESS")
            
            //parse JSON
            js, err := simplejson.NewJson([]byte(body))
            
            if err == nil {
                taskId     := js.Get("task_id").MustString()
                //clusterId  := js.Get("cluster_id").MustString()
                fqdn       := js.Get("fqdn").MustString()
                action     := js.Get("action").MustInt()
                nowTime    := time.Now().UTC().Format(time.RFC3339)
                tenant     := js.Get("tenant").MustString()
                serviceName:= js.Get("service_name").MustString()
                
                returnData["task_id"] = taskId

                //DB connection
                mongoDBDialInfo := &mgo.DialInfo{
                    Addrs:    []string{os.Getenv("MONGODB_HOST")},
                    Timeout:  0 * time.Second,
                    Database: cg.Mongo.AuthDatabase,
                    Username: cg.Mongo.AuthUserName,
                    Password: cg.Mongo.AuthPassword,
                }

                // Create a session which maintains a pool of socket connections
                session, err := mgo.DialWithInfo(mongoDBDialInfo)
                if err == nil {

                    defer session.Close()
                    session.SetMode(mgo.Monotonic, true)
                    mongoConnection := session.DB("ansible").C("backup_task_record")
             
                    var count int
                    //check taskid not exist
                    count, err = mongoConnection.Find(bson.M{"task_id": taskId }).Count()
                    if err != nil {
                        Log(l, logFile, apiName,"[ERROR] MONGO QUERY ERROR")
                        returnData["status"] = "FAILED"
                        returnData["errorcode"] = "BKP00001"
                        returnData["message"] = "Mongo query task_id error"
                   
                    }else if(count == 0){                        
                        
                        //instert task & run command
                        if err == nil {
                            
                            //getHAType
                            session, err := mgo.DialWithInfo(mongoDBDialInfo)
                            defer session.Close()
                            session.SetMode(mgo.Monotonic, true)
                            mongoConnection := session.DB("ansible").C("host")

                            type Task struct {
                                Task_Id        string `bson:"task_Id"`  
                                Cluster_id     string `bson:"cluster_id"` 
                                Function_type  string `bson:"function_type"`
                                Monitor_hosts  []struct {
                                    Fqdn        string `bson:"fqdn"`
                                    Ip          string `bson:"ip"`
                                } `json:"monitor_hosts"`
                                Data_hosts    []struct {
                                    Fqdn        string `bson:"fqdn"`
                                    Ip          string `bson:"ip"`
                                } `bson:"data_hosts"`
                                Datetime       time.Time `bson:"datetime"`
                                Request_member string `bson:"request_member"`
                                Tenant         string `bson:"tenant"`
                                Service_name   string `bson:"service_name"`
                            }

                            task := Task{}
                            err = mongoConnection.Find(bson.M{"data_hosts": bson.M{"$elemMatch": bson.M{"fqdn": fqdn}}}).One(&task)

                            if err != nil {
                                
                                fmt.Println(err)
                                Log(l, logFile, apiName,"[ERROR] CANNOT FIND IT IN HOST INFO")
                                returnData["status"] = "FAILED"
                                returnData["errorcode"] = "BKP00003"
                                returnData["message"] = "cannot find it in host info"
                            
                            } else {

                                defer session.Close()
                                session.SetMode(mgo.Monotonic, true)
                                mongoConnection := session.DB("ansible").C("backup_task_record")
                         
                                type backupTask struct {
                                    Task_Id        string         `json:"task_id"`
                                    Cluster_id     string         `json:"cluster_id"`
                                    Fqdn           string         `json:"fqdn"`
                                    Monitor_hosts  []struct {
                                        Fqdn string `bson:"fqdn"`
                                        Ip   string `bson:"ip"`
                                    } `json:"monitor_hosts"`
                                    Data_hosts     []struct {
                                        Fqdn string `bson:"fqdn"`
                                        Ip   string `bson:"ip"`
                                    } `json:"data_hosts"`
                                    Datetime       string         `json:"datetime"`
                                    Tenant         string         `bson:"tenant"`
                                    Service_name   string         `bson:"service_name"`
                                }

                                err = mongoConnection.Insert(&backupTask{
                                    Task_Id:        taskId,
                                    Cluster_id:     clusterId,
                                    Fqdn:           fqdn,
                                    Monitor_hosts:  task.Monitor_hosts,
                                    Data_hosts:     task.Data_hosts,
                                    Datetime:       nowTime,
                                    Tenant:         tenant,
                                    Service_name:   serviceName,
                                })

                                typeCheck := false
                                args := []string{}
                                
                                //call python command
                                //judge function type to different command
                                switch task.Function_type {
                                    case "mysql_standAlone":

                                        typeCheck = true
                                        fqdn := task.Data_hosts[0].Fqdn
                                        ip   := task.Data_hosts[0].Ip
                                        
                                        if action == 1{
                                            args = []string{"dbbackup", "enable" ,"--dbtype", "mysql", "--data_host", fqdn+":"+ip, "--dbfqdn", fqdn,  "--taskid", taskId, "--cluster_id", clusterId}

                                            
                                        }else if action == 0{
                                            args = []string{"dbbackup", "disable" ,"--dbtype", "mysql", "--data_host", fqdn+":"+ip, "--dbfqdn", fqdn, "--taskid", taskId, "--cluster_id", clusterId}
                                        }

                                        if serviceName != "" {
                                            args = append(args, "--service_name", serviceName)
                                        }

                                        if tenant != "" {
                                            args = append(args, "--tenant", tenant)
                                        }

                                        //pyansibleinv dbbackup enable --dbtype mysql --data_host dcs-db-c00aee9f.iad1:10.41.234.57 --dbfqdn dcs-db-c00aee9f.iad1
                                        //enable, disable, status, list
                                        break

                                    case "myssql_standAlone":
                                        typeCheck = true

                                        args = []string{"dbbackup", "enable" ,"--dbtype", "mysql_mha", "--taskid", taskId, "--dbfqdn", fqdn+":"+ip}

                                        break

                                    default:
                                        typeCheck = false
                                        Log(l, logFile, apiName,"[ERROR] NOT DEFINE TYPE")
                                        returnData["status"] = "FAILED"
                                        returnData["errorcode"] = "BKP00004"
                                        returnData["message"] = "Using not define function type"
                                        break
                                }

                                if typeCheck == true {
                            
                                    //In this place Ansible only can return message in error,  so we parse error message and mark the following error checking
                                    /* 
                                    if err != nil {
                                        Log(l, logFile, apiName,"[ERROR] RUN ANSIBLE COMMAND ERROR")
                                        Log(l, logFile, apiName, err.Error())
                                        returnData["status"] = "FAILED"
                                        returnData["errorcode"] = "BKP00005"
                                        returnData["message"] = "Run ansible playbook error"
                                    }else{
                                    */
                                        //get ansible return error to parse
                                        cmdPath := "/usr/bin/pyansibleinv" 
                                        jsonResult, err := getCmdJsonResult(cmdPath, args, apiName, logFile, l)
                                        
                                        if err != nil {
                                            fmt.Println(err)
                                            Log(l, logFile, apiName,"[ERROR] BACKUP ANSIBLE RETURN FORMAT IS NOT JSON")
                                            Log(l, logFile, apiName, err.Error())
                                            returnData["status"] = "FAILED"
                                            returnData["errorcode"] = "BKP00007"
                                            returnData["reson"] = "backup ansible return format is not json"
                                        
                                        }else{

                                            data := []map[string]interface{}{}

                                            for k, v := range jsonResult {
                                                
                                                switch t := v.(type) {
                                                    case map[string]interface{}:
                                                        
                                                        if value, ok := v.(map[string]interface{}); ok {
                                                            value["fqdn"] = k
                                                            data = append(data, value)
                                                        }
                                                    case string:
                                                              fmt.Println("string",t)
                                                    default:
                                                              fmt.Println("default",t)
                                                }
                                            }

                                            Log(l, logFile, apiName,"RUN BACKUP COMMAND SUCCESS")
                                            returnData["status"] = "SUCCESSFUL"
                                            returnData["message"] = data
                                        }
                                }

                            }

                        }else {
                            Log(l, logFile, apiName,"[ERROR] MONGO insert ERROR")
                            fmt.Println(err)
                            returnData["status"] = "FAILED"
                            returnData["errorcode"] = "BKP00005"
                            returnData["message"] = "Mongodb insert error"
                        }

                    }else{
                        Log(l, logFile, apiName,"[ERROR] BACKUP TASK_ID ALREADY EXIST")
                        returnData["status"] = "FAILED"
                        returnData["errorcode"] = "BKP00002"
                        returnData["message"] = "Backup task_id already exist"
                    }

                }else{
                    Log(l, logFile, apiName,"[ERROR] MONGODB CONNECTION ERROR")
                    fmt.Println(err)
                    returnData["status"] = "FAILED"
                    returnData["errorcode"] = "BKP00006"
                    returnData["message"] = "Mongodb connection error"
                }

            }else{
                Log(l, logFile, apiName,"[ERROR] JSON FORMAT ERROR")
                fmt.Println(err)
                returnData["status"] = "FAILED"
                returnData["errorcode"] = "GLB00002"
                returnData["message"] = "JSON format error"
            }
                
        } else  {
            Log(l, logFile, apiName,"[ERROR] TOKEN AUTH FAILED")
            returnData["status"] = "FAILED"
            returnData["errorcode"] = "GLB00001"
            returnData["message"] = "Auth faild"
        }

        c.JSON(200, returnData)
    }
}

func v1_getDatabaseBackupStatus(c *gin.Context, l *log.Logger, logFile string, cg CONFIG) {

    apiName    := "GetDatabaseBackupStatus"
    token      := c.Request.Header.Get("Authorization")
    returnData := make(map[string]interface{})
    clusterId  := c.Param("cluster_id")

    var forwardResponse []byte
    var statusCode int
    dataCenter := c.Query("datacenter")

    if dataCenter != "" {

        forwardHost := getForwardHost(dataCenter)
        forWardUrl := "http://"+forwardHost+"/v1/ansible/databases/"+clusterId+"/backup"
        forwardResponse, statusCode = httpRequest(token, "GET", forWardUrl, "", apiName, l, logFile)

        c.Data(statusCode, "application/json; charset=utf-8", forwardResponse)
        
    }else{
   
        if token == os.Getenv("TOKEN") {

            //log.Print("AUTH SUCCESS")
            Log(l, logFile, apiName,"[SUCCESS] AUTH SUCCESS")

            if clusterId != ""{

                //DB connection
                mongoDBDialInfo := &mgo.DialInfo{
                    Addrs:    []string{os.Getenv("MONGODB_HOST")},
                    Timeout:  0 * time.Second,
                    Database: cg.Mongo.AuthDatabase,
                    Username: cg.Mongo.AuthUserName,
                    Password: cg.Mongo.AuthPassword,
                }

                 //getHAType
                session, err := mgo.DialWithInfo(mongoDBDialInfo)
                defer session.Close()
                session.SetMode(mgo.Monotonic, true)
                mongoConnection := session.DB("ansible").C("host")

                type Task struct {
                    Task_Id        string `bson:"task_Id"`  
                    Cluster_id     string `bson:"cluster_id"` 
                    Function_type  string `bson:"function_type"`
                    Monitor_hosts  []struct {
                        Fqdn string `bson:"fqdn"`
                        Ip   string `bson:"ip"`
                    } `json:"monitor_hosts"`
                    Data_hosts    []struct {
                        Fqdn string `bson:"fqdn"`
                        Ip   string `bson:"ip"`
                    } `bson:"data_hosts"`
                    Datetime       time.Time `bson:"datetime"`
                    Tenant         string   `bson:"tenant"`
                    Service_name   string   `bson:"service_name"`
                }


                task := Task{}
                err = mongoConnection.Find(bson.M{"cluster_id": clusterId}).One(&task)

                if err == nil{
                    fqdn := task.Data_hosts[0].Fqdn
                    ip   := task.Data_hosts[0].Ip                        

                    args := []string{"dbbackup", "status" ,"--dbtype", "mysql", "--data_host", fqdn+":"+ip, "--dbfqdn", fqdn, "--cluster_id", clusterId}


                    //execute Ansible
                    cmdPath := "/usr/bin/pyansibleinv" 
                    jsonResult, err := getCmdJsonResult(cmdPath, args, apiName, logFile, l)
                    
                    if err != nil {
                        fmt.Println(err)
                        Log(l, logFile, apiName,"[ERROR] BACKUP ANSIBLE RETURN FORMAT IS NOT JSON")
                        Log(l, logFile, apiName, err.Error())
                        returnData["status"] = "FAILED"
                        returnData["errorcode"] = "BKP00007"
                        returnData["reson"] = "backup ansible return format is not json"
                    
                    }else{

                    //In this place Ansible only can return message in error,  so we parse error message and mark the following error checking
                    /* 
                    if err != nil {
                        Log(l, logFile, apiName,"[ERROR] RUN GET BACKUP STATUS COMMAND ERROR")
                        fmt.Println(err)
                        returnData["status"] = "FAILED"
                        returnData["errorcode"] = "GBK00005"
                        returnData["reson"] = "Run get backup status command error"
                    }else{
                    */

                        data := []map[string]interface{}{}

                        for k, v := range jsonResult {
                            
                            switch t := v.(type) {
                                case map[string]interface{}:
                                    
                                    if value, ok := v.(map[string]interface{}); ok {
                                        value["fqdn"] = k
                                        data = append(data, value)
                                    }
                                case string:
                                          fmt.Println("string",t)
                                default:
                                          fmt.Println("default",t)
                            }
                        }

                        Log(l, logFile, apiName,"RUN GET BACKUP STATUS COMMAND SUCCESS")
                        returnData["status"] = "SUCCESSFUL"
                        returnData["message"] = data
                    }
                } else {
                    Log(l, logFile, apiName,"[ERROR] CANNOT FIND THIS CLUSTER INFO IN RECORD")
                    returnData["status"] = "FAILED"
                    returnData["errorcode"] = "GBK00002"
                    returnData["message"] = "Cannot find this cluster info in record"
                }   
 
            } else {
                Log(l, logFile, apiName,"[ERROR] NO CLUSTER ID IN URL")
                returnData["status"] = "FAILED"
                returnData["errorcode"] = "GBK00001"
                returnData["message"] = "No cluster_id in url"
            }

        } else  {
            Log(l, logFile, apiName,"[ERROR] TOKEN AUTH FAILED")
            returnData["status"] = "FAILED"
            returnData["errorcode"] = "GLB00001"
            returnData["message"] = "Auth faild"
        }

        c.JSON(200, returnData)
    }
}

func v1_getDatabaseBackupList(c *gin.Context, l *log.Logger, logFile string, cg CONFIG) {
    
    apiName    := "GetDatabaseBackupList"
    token      := c.Request.Header.Get("Authorization")
    returnData := make(map[string]interface{})
    clusterId  := c.Param("cluster_id")

    var forwardResponse []byte
    var statusCode int
    dataCenter := c.Query("datacenter")

    if dataCenter != "" {

        forwardHost := getForwardHost(dataCenter)
        forWardUrl := "http://"+forwardHost+"/v1/ansible/databases/"+clusterId+"/backuplist"
        forwardResponse, statusCode = httpRequest(token, "GET", forWardUrl, "", apiName, l, logFile)

        c.Data(statusCode, "application/json; charset=utf-8", forwardResponse)
        
    }else{
   
        if token == os.Getenv("TOKEN") {

            //log.Print("AUTH SUCCESS")
            Log(l, logFile, apiName,"[SUCCESS] AUTH SUCCESS")

            if clusterId != ""{

                //DB connection
                mongoDBDialInfo := &mgo.DialInfo{
                    Addrs:    []string{os.Getenv("MONGODB_HOST")},
                    Timeout:  0 * time.Second,
                    Database: cg.Mongo.AuthDatabase,
                    Username: cg.Mongo.AuthUserName,
                    Password: cg.Mongo.AuthPassword,
                }

                 //getHAType
                session, err := mgo.DialWithInfo(mongoDBDialInfo)
                defer session.Close()
                session.SetMode(mgo.Monotonic, true)
                mongoConnection := session.DB("ansible").C("host")

                type Task struct {
                    Task_Id        string `bson:"task_Id"`  
                    Cluster_id     string `bson:"cluster_id"` 
                    Function_type  string `bson:"function_type"`
                    Monitor_hosts  []struct {
                        Fqdn string `bson:"fqdn"`
                        Ip   string `bson:"ip"`
                    } `json:"monitor_hosts"`
                    Data_hosts    []struct {
                        Fqdn string `bson:"fqdn"`
                        Ip   string `bson:"ip"`
                    } `bson:"data_hosts"`
                    Datetime       time.Time `bson:"datetime"`
                    Tenant         string   `bson:"tenant"`
                    Service_name   string   `bson:"service_name"`
                }

                task := Task{}
                err = mongoConnection.Find(bson.M{"cluster_id": clusterId}).One(&task)
                
                if err == nil{
                    fqdn := task.Data_hosts[0].Fqdn
                    ip   := task.Data_hosts[0].Ip                        

                    args := []string{"dbbackup", "listx" ,"--dbtype", "mysql", "--data_host", fqdn+":"+ip, "--dbfqdn", fqdn, "--cluster_id", clusterId}


                    //execute Ansible
                    cmdPath := "/usr/bin/pyansibleinv" 
                    jsonResult, err := getCmdJsonResult(cmdPath, args, apiName, logFile, l)
                    
                    if err != nil {
                        fmt.Println(err)
                        Log(l, logFile, apiName,"[ERROR] BACKUP ANSIBLE RETURN FORMAT IS NOT JSON")
                        Log(l, logFile, apiName, err.Error())
                        returnData["status"] = "FAILED"
                        returnData["errorcode"] = "BKP00007"
                        returnData["reson"] = "backup ansible return format is not json"
                    
                    }else{

                    //In this place Ansible only can return message in error, so we parse error message and mark the following error checking
                    /* 
                    if err != nil {
                        Log(l, logFile, apiName,"[ERROR] RUN GET BACKUP STATUS COMMAND ERROR")
                        fmt.Println(err)
                        returnData["status"] = "FAILED"
                        returnData["errorcode"] = "GBK00005"
                        returnData["reson"] = "Run get backup status command error"
                    }else{
                    */
                        //get ansible return error to parse
                        
                        data := []map[string]interface{}{}

                        for k, v := range jsonResult {
                            
                            switch t := v.(type) {
                                case map[string]interface{}:
                                    
                                    if value, ok := v.(map[string]interface{}); ok {
                                        value["fqdn"] = k
                                        data = append(data, value)
                                    }
                                case string:
                                          fmt.Println("string",t)
                                default:
                                          fmt.Println("default",t)
                            }
                        }                     

                        Log(l, logFile, apiName,"RUN GET BACKUP STATUS COMMAND SUCCESS")
                        returnData["status"] = "SUCCESSFUL"
                        returnData["message"] = data
                    }
                } else {
                    Log(l, logFile, apiName,"[ERROR] CANNOT FIND THIS CLUSTER INFO IN RECORD")
                    returnData["status"] = "FAILED"
                    returnData["errorcode"] = "GBK00002"
                    returnData["message"] = "Cannot find this cluster info in record"
                }
 
            } else {
                Log(l, logFile, apiName,"[ERROR] NO TASK_ID IN URL")
                returnData["status"] = "FAILED"
                returnData["errorcode"] = "GBL00001"
                returnData["message"] = "No cluster_id in url"
            }

        } else  {
            Log(l, logFile, apiName,"[ERROR] TOKEN AUTH FAILED")
            returnData["status"] = "FAILED"
            returnData["errorcode"] = "GLB00001"
            returnData["message"] = "Auth faild"
        }

        c.JSON(200, returnData)
    }
}