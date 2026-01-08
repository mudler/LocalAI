const canvas = document.getElementById('networkCanvas');
const ctx = canvas.getContext('2d');

let particles = [];
let isDragging = false;
let dragParticle = null;
const maxParticles = 100; // Maximum number of particles
const dragAreaRadius = 10; // Increased area for easier dragging

// Function to resize canvas based on aspect ratio
function resizeCanvas() {
    canvas.width = window.innerWidth;
    canvas.height = Math.min(window.innerHeight, 400); // Maintain a max height of 400px
}

// Adjust the canvas size when the window resizes
window.addEventListener('resize', resizeCanvas);

// Initialize canvas size
resizeCanvas();

class Particle {
    constructor(x, y) {
        this.x = x;
        this.y = y;
        this.radius = 4;
        this.color = `rgba(0, 255, 204, 1)`;
        this.speedX = (Math.random() - 0.5) * 2; // Random horizontal speed
        this.speedY = (Math.random() - 0.5) * 2; // Random vertical speed
    }

    update() {
        if (!isDragging || dragParticle !== this) {
            this.x += this.speedX;
            this.y += this.speedY;

            // Bounce off the edges of the canvas
            if (this.x < 0 || this.x > canvas.width) {
                this.speedX *= -1;
            }
            if (this.y < 0 || this.y > canvas.height) {
                this.speedY *= -1;
            }
        }
    }

    draw() {
        ctx.beginPath();
        ctx.arc(this.x, this.y, this.radius, 0, Math.PI * 2, false);
        ctx.fillStyle = this.color;
        ctx.fill();
    }

    isMouseOver(mouseX, mouseY) {
        // Increase the draggable area by checking if the mouse is within a larger radius
        return Math.hypot(mouseX - this.x, mouseY - this.y) < dragAreaRadius;
    }
}

function connectParticles() {
    for (let i = 0; i < particles.length; i++) {
        for (let j = i + 1; j < particles.length; j++) {
            const distance = Math.hypot(particles[i].x - particles[j].x, particles[i].y - particles[j].y);
            if (distance < 150) {
                ctx.beginPath();
                ctx.moveTo(particles[i].x, particles[i].y);
                ctx.lineTo(particles[j].x, particles[j].y);
                ctx.strokeStyle = `rgba(0, 255, 204, ${1 - distance / 150})`;
                ctx.stroke();
            }
        }
    }
}

function initParticles(num) {
    for (let i = 0; i < num; i++) {
        particles.push(new Particle(Math.random() * canvas.width, Math.random() * canvas.height));
    }
}

function animate() {
    ctx.clearRect(0, 0, canvas.width, canvas.height);

    particles.forEach(particle => {
        particle.update();
        particle.draw();
    });

    connectParticles();

    requestAnimationFrame(animate);
}

// Handle mouse click to create a new particle
canvas.addEventListener('click', (e) => {
    const rect = canvas.getBoundingClientRect();
    const mouseX = e.clientX - rect.left;
    const mouseY = e.clientY - rect.top;

    const newParticle = new Particle(mouseX, mouseY);
    particles.push(newParticle);

    // Limit the number of particles to the maximum
    if (particles.length > maxParticles) {
        particles.shift(); // Remove the oldest particle
    }
});

// Handle mouse down for dragging
canvas.addEventListener('mousedown', (e) => {
    const rect = canvas.getBoundingClientRect();
    const mouseX = e.clientX - rect.left;
    const mouseY = e.clientY - rect.top;

    for (let particle of particles) {
        if (particle.isMouseOver(mouseX, mouseY)) {
            isDragging = true;
            dragParticle = particle;
            break;
        }
    }
});

// Handle mouse move for dragging
canvas.addEventListener('mousemove', (e) => {
    if (isDragging && dragParticle) {
        const rect = canvas.getBoundingClientRect();
        const mouseX = e.clientX - rect.left;
        const mouseY = e.clientY - rect.top;

        dragParticle.x = mouseX;
        dragParticle.y = mouseY;
    }
});

// Handle mouse up to stop dragging
canvas.addEventListener('mouseup', () => {
    isDragging = false;
    dragParticle = null;
});

// Initialize and start the animation
initParticles(maxParticles);
animate();