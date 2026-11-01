#include "engine/framework/audio/fft.h"
#include "engine/framework/runtime/session.h"
#include <atomic>
#include <cmath>
#include <complex>
#include <cstring>
#include <iostream>
#include <stdexcept>
#include <thread>

using namespace engine::audio;
namespace {
void require(bool b, const char * msg) { if (!b) throw std::runtime_error(msg); }
std::atomic<bool> stop{false};
std::atomic<size_t> units{0}, vectors{0}, scalars{0}, workers{0};
std::atomic<int> target{-1}, fault{0};
std::atomic<bool> external_request{false}, observed_write{false};
std::atomic<bool> entered{false}, release_worker{false}, settled{false};
bool requested(void *) noexcept { return stop.load(); }
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
    stop=false; target=-1; units=0;
    require(plan.forward({count,n},{128,4},{136,8},1,in.data(),out.data(),1,threads,{requested,nullptr})==FFTStatus::Completed,"fresh same plan");
    require(units==count,"fresh complete groups");
}
void external_stop() {
    reset(); observed_write=false; external_request=true;
    std::thread requester([] { while(!observed_write.load()) std::this_thread::yield(); stop=true; });
    std::vector<float> in(1024*32,.25f);
    std::vector<std::complex<float>> out(1024*17,sentinel);
    auto status=real_fft_forward({1024,32},{128,4},{136,8},1,in.data(),out.data(),1,4,{requested,nullptr});
    requester.join(); external_request=false;
    require(status==FFTStatus::Cancelled && units>0 && units<1024,"external active stop");
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
    std::thread owner([&]{
        std::vector<float> in(1024*32);
        std::vector<std::complex<float>> out(1024*17);
        try { real_fft_forward({1024,32},{128,4},{136,8},1,in.data(),out.data(),1,4,{requested,nullptr}); }
        catch(const std::runtime_error &) { wrong=!settled.load(); caught=true; }
    });
    // Handshake, not a timing race. The CTest timeout is only an outer failure cap.
    while (!entered.load()) std::this_thread::yield();
    require(!caught,"early owner exception");
    release_worker=true;
    owner.join();
    require(caught && !wrong,"join before exception");
}
}
namespace audio_cpp_fft_internal::testing {
void stored(bool vector, size_t n, size_t, size_t nw) noexcept {
    units.fetch_add(n); (vector?vectors:scalars).fetch_add(1);
    size_t old=workers; while(old<nw && !workers.compare_exchange_weak(old,nw)) {}
    if(external_request.load()) {
        observed_write=true;
        while(!stop.load()) std::this_thread::yield();
    }
    if(target==(vector?1:0)) stop=true;
}
void worker() {
    int f=fault.load();
    if(f==1) {
        bool expected=false;
        if(entered.compare_exchange_strong(expected,true)) {
            while(!release_worker.load()) std::this_thread::yield();
            settled=true;
            throw std::runtime_error("injected worker scratch failure");
        }
    } else if(f==2) {
        entered=true;
        while(!release_worker.load()) std::this_thread::yield();
        settled=true;
    }
}
void submitting(size_t i) {
    if(fault==2 && i==1) {
        while(!entered.load()) std::this_thread::yield();
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
    external_stop(); inverse_check();
    fault_test(1); fault_test(2);
    std::cout<<"FFT cancellation/numerics/fault joins PASS\n";
 } catch(const std::exception & e) { std::cerr<<"FAIL: "<<e.what()<<"\n"; return 1; }
}
