#include "engine/framework/audio/fft.h"
#include "engine/framework/runtime/session.h"
#include <atomic>
#include <cmath>
#include <complex>
#include <cstring>
#include <iostream>
#include <stdexcept>
#include <thread>
#include <chrono>
#include <condition_variable>
#include <mutex>
#include <cstdlib>
#include <exception>

using namespace engine::audio;
namespace {
void require(bool b, const char * msg) { if (!b) throw std::runtime_error(msg); }
std::atomic<bool> stop{false};
std::atomic<size_t> units{0}, vectors{0}, scalars{0}, workers{0};
std::atomic<int> target{-1}, fault{0};
std::atomic<bool> external_request{false}, observed_write{false};
std::atomic<bool> entered{false}, release_worker{false}, settled{false};
struct OwnedStop {
    engine::core::RunStopHandle handle=std::make_shared<engine::core::RunStopState>();
    void cancel() noexcept { handle->cancel(); }
    bool cancelled() const noexcept { return handle->cancelled(); }
};
bool requested(void * p) noexcept { return p ? static_cast<OwnedStop *>(p)->cancelled() : stop.load(); }
OwnedStop * active_owned=nullptr;
OwnedStop * late_a=nullptr;
std::atomic<size_t> watched_n{0}, misses{0}, publications{0}, worker_entries{0};
std::atomic<bool> cold_stop{false}, watchdog_failed{false};
std::mutex gate_mutex;
std::condition_variable gate_cv;
void release_all() noexcept {
    stop=true; release_worker=true; entered=true; observed_write=true;
    gate_cv.notify_all();
}
bool wait_flag(const std::atomic<bool> & flag) noexcept {
    std::unique_lock<std::mutex> lock(gate_mutex);
    bool ok=gate_cv.wait_until(lock,std::chrono::steady_clock::now()+std::chrono::seconds(2),[&]{return flag.load();});
    if(!ok) { watchdog_failed=true; release_all(); }
    return ok;
}
// Both the normal and exceptional exits release observers, then join every thread.
struct Handshake {
    std::thread participant, watchdog;
    bool finished=false;
    Handshake() {
        watchdog_failed=false;
        watchdog=std::thread([this]{
            std::unique_lock<std::mutex> lock(gate_mutex);
            if(!gate_cv.wait_until(lock,std::chrono::steady_clock::now()+std::chrono::seconds(2),[&]{return finished;})) {
                watchdog_failed=true; release_all();
            }
        });
    }
    ~Handshake() {
        release_all();
        if(participant.joinable()) participant.join();
        { std::lock_guard<std::mutex> lock(gate_mutex); finished=true; }
        gate_cv.notify_all(); watchdog.join();
    }
    void join() { if(participant.joinable()) participant.join(); }
};
void reset() { stop=false; units=0; vectors=0; scalars=0; workers=0; target=-1; fault=0; }
float wave(size_t j) { return float(int(j*13%31)-15)/32; }
constexpr std::complex<float> sentinel{1234,5678};
double max_error=0;
void numerical(size_t n, size_t batches, size_t threads, float scale, bool strided) {
    reset();
    size_t si=strided?2:1, so=strided?3:1, bins=n/2+1;
    std::vector<float> in(batches*n*si+2, 999);
    std::vector<std::complex<float>> a(batches*bins*so+2,sentinel), b=a;
    for (size_t t=0;t<batches;++t) for(size_t j=0;j<n;++j) in[1+(t*n+j)*si]=wave(t+j);
    TensorShape shape=batches==1?TensorShape{n}:TensorShape{2,batches/2,n};
    TensorStrideBytes is=batches==1?TensorStrideBytes{std::ptrdiff_t(si*4)}:TensorStrideBytes{std::ptrdiff_t(batches/2*n*si*4),std::ptrdiff_t(n*si*4),std::ptrdiff_t(si*4)};
    TensorStrideBytes os=batches==1?TensorStrideBytes{std::ptrdiff_t(so*8)}:TensorStrideBytes{std::ptrdiff_t(batches/2*bins*so*8),std::ptrdiff_t(bins*so*8),std::ptrdiff_t(so*8)};
    size_t axis=shape.size()-1;
    real_fft_forward(shape,is,os,axis,in.data()+1,a.data()+1,scale,threads);
    require(real_fft_forward(shape,is,os,axis,in.data()+1,b.data()+1,scale,threads,{requested,nullptr})==FFTStatus::Completed,"empty completed");
    require(std::memcmp(a.data(),b.data(),a.size()*sizeof(a[0]))==0,"legacy bitwise");
    for(size_t t=0;t<batches;++t) for(size_t k=0;k<bins;++k) {
        std::complex<double> expected{};
        for(size_t j=0;j<n;++j) { double angle=-2*std::acos(-1.)*double(k*j)/double(n); expected+=double(wave(t+j))*std::complex<double>(std::cos(angle),std::sin(angle))*double(scale); }
        auto actual=std::complex<double>(b[1+(t*bins+k)*so]);
        max_error=std::max(max_error,std::abs(actual-expected));
        require(std::abs(actual-expected)<=2e-5+2e-5*std::abs(expected),"DFT mismatch");
    }
    for(size_t j=0;j<b.size();++j) if(j==0 || j> batches*bins*so || (j-1)%so) require(b[j]==sentinel,"output canary");
    for(size_t j=0;j<in.size();++j) if(j==0 || j>batches*n*si || (j-1)%si) require(in[j]==999,"input canary");
}
void cancellation(size_t count, size_t threads, int path, bool pre=false) {
    reset(); target=path; stop=pre;
    const size_t n=32,bins=17;
    std::vector<float> in(count*n); for(size_t j=0;j<in.size();++j) in[j]=wave(j);
    std::vector<std::complex<float>> out(count*bins,sentinel);
    RealFFTPlan plan(n);
    auto status=plan.forward({count,n},{128,4},{136,8},1,in.data(),out.data(),1,threads,{requested,nullptr});
    require(status==FFTStatus::Cancelled,"cancelled status");
    size_t done=units;
    if (!pre) require((path==1?vectors.load():scalars.load())<=threads,"target path missing poll");
    try { if(status==FFTStatus::Cancelled) throw engine::runtime::RunCancelled(); }
    catch(const engine::runtime::RunCancelled &) { require(units==done,"owner cancellation before join"); }
    require(pre?done==0:(done>0 && done<count),"active partial writes");
    if (!pre) { bool changed=false; for(auto v:out) changed |= v!=sentinel; require(changed,"real writes"); }
    for(size_t i=0;i<1000;++i) std::this_thread::yield();
    require(units==done,"progress after return");

}
void external_stop() {
    reset(); observed_write=false; external_request=true;
    Handshake guard;
    guard.participant=std::thread([] { wait_flag(observed_write); stop=true; gate_cv.notify_all(); });
    std::vector<float> in(1024*32,.25f);
    std::vector<std::complex<float>> out(1024*17,sentinel);
    auto status=real_fft_forward({1024,32},{128,4},{136,8},1,in.data(),out.data(),1,4,{requested,nullptr});
    guard.join(); external_request=false;
    require(!watchdog_failed,"external watchdog missing event");
    require(status==FFTStatus::Cancelled && units>0 && units<1024,"external active stop");
}
struct Fixture {
    size_t n, count, si=2, so=3, bins;
    TensorShape shape;
    TensorStrideBytes is, os;
    std::vector<float> in;
    std::vector<std::complex<float>> out;
    Fixture(size_t size, size_t c, bool rank1=false):n(size),count(c),bins(n/2+1),
        shape(rank1?TensorShape{n}:TensorShape{2,count/2,n}),
        is(rank1?TensorStrideBytes{8}:TensorStrideBytes{std::ptrdiff_t(count/2*n*8),std::ptrdiff_t(n*8),8}),
        os(rank1?TensorStrideBytes{24}:TensorStrideBytes{std::ptrdiff_t(count/2*bins*24),std::ptrdiff_t(bins*24),24}),
        in(count*n*si+2,999),out(count*bins*so+2,sentinel) {
        for(size_t t=0;t<count;++t) for(size_t j=0;j<n;++j) in[1+(t*n+j)*si]=wave(t+j);
    }
    FFTStatus run(RealFFTPlan & plan, OwnedStop & owned) {
        return plan.forward(shape,is,os,shape.size()-1,in.data()+1,out.data()+1,.125f,4,{requested,&owned});
    }
    FFTStatus free_run() {
        return real_fft_forward(shape,is,os,shape.size()-1,in.data()+1,out.data()+1,.125f,4,{requested,nullptr});
    }
    void dft() {
        for(size_t t=0;t<count;++t) for(size_t k=0;k<bins;++k) {
            std::complex<double> expected{};
            for(size_t j=0;j<n;++j) {
                double a=-2*std::acos(-1.)*double(k*j)/double(n);
                expected+=double(wave(t+j))*.125*std::complex<double>(std::cos(a),std::sin(a));
            }
            double e=std::abs(std::complex<double>(out[1+(t*bins+k)*so])-expected);
            max_error=std::max(max_error,e);
            require(e<=2e-5+2e-5*std::abs(expected),"recovered DFT mismatch");
        }
        for(size_t j=0;j<out.size();++j) if(j==0 || j>count*bins*so || (j-1)%so) require(out[j]==sentinel,"recovery output canary");
        for(size_t j=0;j<in.size();++j) if(j==0 || j>count*n*si || (j-1)%si) require(in[j]==999,"recovery input canary");
    }
};
void retained_recovery() {
    reset();
    RealFFTPlan plan(32);
    OwnedStop a,b; active_owned=&a; target=1;
    Fixture cancelled(32,258);
    require(cancelled.run(plan,a)==FFTStatus::Cancelled,"owned A cancelled");
    require(a.cancelled() && units>0 && units<258,"owned A partial work");
    target=-1; units=0; active_owned=&b; late_a=&a;
    Fixture recovered(32,258);
    auto status=recovered.run(plan,b);
    if(std::getenv("FFT_CORRUPT_RECOVERY")) recovered.out[1]+=1.f;
    late_a=nullptr; active_owned=nullptr;
    require(status==FFTStatus::Completed && units==258 && !b.cancelled(),"independent B late A isolation");
    recovered.dft();
    // A second rank-one strided recovery on the exact same retained plan.
    Fixture rank1(32,1,true);
    require(rank1.run(plan,b)==FFTStatus::Completed,"rank1 recovered completion"); rank1.dft();
    std::cout<<"owned A/B retained plan recovery DFT rank3/rank1 strided scale=.125 PASS\n";
}
void cold_acquisition() {
    // These unique sizes never occur in the numerical sweep; no plan is prebuilt.
    for(size_t n:{37,41}) {
        reset(); watched_n=n; misses=0; publications=0; worker_entries=0;
        Fixture f(n,2);
        stop=true;
        require(f.free_run()==FFTStatus::Cancelled,"cold pre-stopped status");
        require(misses==0 && publications==0 && units==0,"cold pre-stop constructed plan");
        stop=false; cold_stop=true;
        require(f.free_run()==FFTStatus::Cancelled,"cold construction boundary cancellation");
        cold_stop=false;
        require(misses==1 && publications==1 && units==0 && worker_entries==0,"cold constructor completion/post-check");
        for(auto v:f.out) require(v==sentinel,"cold cancelled wrote output");
        stop=false;
        require(f.free_run()==FFTStatus::Completed,"cold recovered completion"); f.dft();
        require(misses==1 && publications==1 && units==2,"warm same N unexpectedly missed");
        std::cout<<"cold N="<<n<<" actual misses="<<misses<<" publications="<<publications<<" cancelled groups=0 warm recovered units="<<units<<" DFT PASS\n";
    }
    watched_n=0;
}
void inverse_check() {
    reset();
    for(size_t n:{8,15,32,64}) {
        std::vector<float> in(n), out(n); for(size_t j=0;j<n;++j) in[j]=wave(j);
        std::vector<std::complex<float>> freq(n/2+1);
        real_fft_forward({n},{4},{8},0,in.data(),freq.data(),1,1);
        real_fft_inverse({n},{8},{4},0,freq.data(),out.data(),1.f/n,1);
        for(size_t j=0;j<n;++j) require(std::abs(in[j]-out[j])<2e-5,"legacy inverse");
    }
}
void fault_test(int which) {
    reset(); fault=which; entered=false; release_worker=false; settled=false;
    // A separate owner must not expose its exception before accepted workers end.
    std::atomic<bool> caught{false}, wrong{false};
    std::exception_ptr unexpected;
    Handshake guard;
    guard.participant=std::thread([&]{
        try {
            std::vector<float> in(1024*32);
            std::vector<std::complex<float>> out(1024*17);
            real_fft_forward({1024,32},{128,4},{136,8},1,in.data(),out.data(),1,4,{requested,nullptr});
        }
        catch(const std::runtime_error &) { wrong=!settled.load(); caught=true; }
        catch(...) { unexpected=std::current_exception(); release_all(); }
    });
    // Handshake, not a timing race. The CTest timeout is only an outer failure cap.
    wait_flag(entered);
    require(!watchdog_failed,"fault watchdog missing event");
    require(!caught,"early owner exception");
    release_worker=true; gate_cv.notify_all();
    guard.join();
    if(unexpected) std::rethrow_exception(unexpected);
    require(!watchdog_failed,"fault watchdog missing event");
    require(caught && !wrong,"join before exception");
}
}
namespace audio_cpp_fft_internal::testing {
void stored(bool vector, size_t n, size_t, size_t nw) noexcept {
    units.fetch_add(n); (vector?vectors:scalars).fetch_add(1);
    size_t old=workers; while(old<nw && !workers.compare_exchange_weak(old,nw)) {}
    if(external_request.load()) {
        if(!std::getenv("FFT_MISSING_EVENT")) observed_write=true;
        gate_cv.notify_all();
        wait_flag(stop);
    }
    if(late_a) {
        late_a->cancel(); // only after B really stored a group
        if(std::getenv("FFT_OLD_A_AFFECTS_B") && active_owned) active_owned->cancel();
    }
    if(target==(vector?1:0)) {
        if(active_owned) active_owned->cancel(); else stop=true;
    }
}
void plan_miss(size_t n, bool published) noexcept {
    if(n!=watched_n.load()) return;
    if(published) ++publications;
    else { ++misses; if(cold_stop) { stop=true; } }
}
void worker() {
    ++worker_entries;
    int f=fault.load();
    if(f==1) {
        bool expected=false;
        if(entered.compare_exchange_strong(expected,true)) {
            if(std::getenv("FFT_MISSING_FAULT_EVENT")) entered=false;
            gate_cv.notify_all();
            wait_flag(release_worker);
            settled=true;
            throw std::runtime_error("injected worker scratch failure");
        }
    } else if(f==2) {
        if(!std::getenv("FFT_MISSING_SUBMIT_EVENT")) entered=true;
        gate_cv.notify_all();
        wait_flag(release_worker);
        settled=true;
    }
}
void submitting(size_t i) {
    if(fault==2 && i==1) {
        wait_flag(entered);
        throw std::runtime_error("injected submission failure");
    }
}
}
int main() {
 try {
    for(size_t n:{8,15,32,64}) for(size_t b:{1,258}) for(size_t th:{1,4}) for(float s:{1.f,.125f}) for(bool stride:{false,true}) numerical(n,b,th,s,stride);
    std::cout<<"independent DFT max absolute error="<<max_error<<" tolerance abs/rel=2e-5\n";
    reset(); numerical(32,258,4,1,false);
    bool vector_available=vectors>0;
    require(scalars>0,"scalar control exercised"); require(workers>1,"actual multiple workers");
    std::cout<<"vector="<<(vector_available?"available":"UNAVAILABLE (not PASS)")<<" workers="<<workers<<" vector groups="<<vectors<<" scalar groups="<<scalars<<"\n";
    cancellation(259,1,0); // scalar remainder after vector work, post-group stop
    cancellation(3,1,0); // independently early scalar-only cancellation
    if(vector_available) { cancellation(258,1,1); cancellation(1024,4,1); }
    cancellation(258,4,0,true);
    retained_recovery(); cold_acquisition();
    external_stop(); inverse_check();
    fault_test(1); fault_test(2);
    std::cout<<"FFT cancellation/numerics/fault joins PASS\n";
 } catch(const std::exception & e) { std::cerr<<"FAIL: "<<e.what()<<"\n"; return 1; }
}
